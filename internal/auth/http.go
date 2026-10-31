package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

const (
	sessionCookie = "accessflow_session"
	csrfCookie    = "accessflow_csrf" // readable by the UI, which echoes it in X-CSRF-Token
	csrfHeader    = "X-CSRF-Token"
)

var (
	errCSRF                   = platform.NewError(http.StatusForbidden, "csrf_failed")
	errPasswordChangeRequired = platform.NewError(http.StatusForbidden, "password_change_required")
)

type principalKey struct{}

// PrincipalFrom returns the signed-in employee set by the session middleware.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Routes serves /api/v1/auth: login, and for any signed-in session (including one
// from a temporary password) me, logout and password change.
func (s *Service) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/login", s.handleLogin)
	r.Post("/activate", s.handleActivate) // no session: the link is the credential, and it signs nobody in
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler { return s.authenticate(true, next) })
		r.Get("/me", s.handleMe)
		r.Post("/logout", s.handleLogout)
		r.Post("/password", s.handlePassword)
	})
	return r
}

// RequireSession guards every other API route: a valid, current session whose password
// is not temporary, and the CSRF header on unsafe methods. An Auditor reaches only the
// audit events, whatever other roles they hold (O7 note 2026-10-31).
func (s *Service) RequireSession(next http.Handler) http.Handler {
	return s.authenticate(false, auditorOnly(next))
}

// auditorRoutes are the only business routes an Auditor may call: reading, filtering and
// exporting the audit events. Exact paths, so nothing else matches by prefix.
var auditorRoutes = map[string]bool{"/api/v1/audit/events": true, "/api/v1/audit/events/export": true, "/api/v1/audit/filters": true}

func auditorOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, _ := PrincipalFrom(r.Context()); p.HasRole("AUDITOR") && (r.Method != http.MethodGet || !auditorRoutes[r.URL.Path]) {
			platform.WriteError(w, r, platform.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole allows only principals holding role; use after RequireSession.
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p, ok := PrincipalFrom(r.Context()); !ok || !p.HasRole(role) {
				platform.WriteError(w, r, platform.ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Service) authenticate(allowTemporary bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			platform.WriteError(w, r, platform.ErrUnauthorized)
			return
		}
		now := time.Now()
		session, err := parseToken(s.secret, cookie.Value, now)
		if err != nil {
			platform.WriteError(w, r, platform.ErrUnauthorized)
			return
		}
		p, err := s.loadPrincipal(r.Context(), session)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		if !safeMethod(r.Method) && subtle.ConstantTimeCompare([]byte(r.Header.Get(csrfHeader)), []byte(session.CSRF)) != 1 {
			platform.WriteError(w, r, errCSRF)
			return
		}
		if p.MustChangePassword && !allowTemporary {
			platform.WriteError(w, r, errPasswordChangeRequired)
			return
		}
		if ShouldReissue(session.LoginAt, session.ExpiresAt, now) {
			if err := s.setCookies(w, session.renewed(now)); err != nil {
				platform.WriteError(w, r, err)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var in struct{ Email, Password string }
	if err := platform.DecodeJSON(w, r, &in); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	session, err := s.login(r.Context(), in.Email, in.Password)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	s.respondWithSession(w, r, session)
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	platform.WriteJSON(w, http.StatusOK, s.principalBody(p))
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	if err := s.logout(r.Context(), p); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	s.clearCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handlePassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ CurrentPassword, NewPassword string }
	if err := platform.DecodeJSON(w, r, &in); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	p, _ := PrincipalFrom(r.Context())
	session, err := s.changePassword(r.Context(), p, in.CurrentPassword, in.NewPassword)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	s.respondWithSession(w, r, session)
}

// respondWithSession sets the cookies for a new session and answers like /me.
func (s *Service) respondWithSession(w http.ResponseWriter, r *http.Request, session Session) {
	p, err := s.loadPrincipal(r.Context(), session)
	if err == nil {
		err = s.setCookies(w, session)
	}
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, s.principalBody(p))
}

func (s *Service) principalBody(p Principal) map[string]any {
	return map[string]any{
		"employee": map[string]any{
			"id":       p.EmployeeID,
			"email":    p.Email,
			"fullName": p.FullName,
			"roles":    p.Roles,
		},
		"adminGroups":        p.AdminGroups,
		"mustChangePassword": p.MustChangePassword,
		"companyTimezone":    s.companyTimezone,
	}
}

func (s *Service) setCookies(w http.ResponseWriter, session Session) error {
	token, err := signToken(s.secret, session)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: session.ExpiresAt,
		HttpOnly: true, Secure: s.secureCookies, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: session.CSRF, Path: "/", Expires: session.ExpiresAt,
		Secure: s.secureCookies, SameSite: http.SameSiteLaxMode})
	return nil
}

func (s *Service) clearCookies(w http.ResponseWriter) {
	for _, name := range []string{sessionCookie, csrfCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1,
			HttpOnly: name == sessionCookie, Secure: s.secureCookies, SameSite: http.SameSiteLaxMode})
	}
}
