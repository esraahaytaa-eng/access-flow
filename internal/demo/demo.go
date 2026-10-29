// Package demo is the local demo seed (A4 and D12 notes 2026-10-29): in demo mode it gives
// the evaluator documented accounts and a realistic company on a clean start. It acts only
// through the real services, at real time, and is idempotent.
package demo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/catalog"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/requests"
	"github.com/esraahaytaa-eng/access-flow/internal/reviews"
)

// Account is a documented demo sign-in; Key names its password in DEMO_PASSWORDS.
type Account struct {
	Key   string `json:"key"`
	Email string `json:"email"`
	Role  string `json:"role"` // the role it demonstrates, in Turkish
}

// Accounts are the documented demo sign-ins, one per main role. The Super Admin is the
// initial Super Admin (auth.Bootstrap).
var Accounts = []Account{
	{"admin", "admin@accessflow.local", "Süper Yönetici"},
	{"infosec", "selin.aydin@accessflow.local", "Bilgi Güvenliği"},
	{"hr", "ayse.demir@accessflow.local", "İK Yöneticisi"},
	{"auditor", "gizem.arslan@accessflow.local", "Denetçi"},
	{"manager", "burak.sahin@accessflow.local", "Yönetici"},
	{"owner", "can.ozturk@accessflow.local", "Araç sahibi ve BT Operasyon"},
	{"employee", "deniz.kara@accessflow.local", "Çalışan"},
}

// AdminName is the demo Super Admin's name.
const AdminName = "Okan Yılmaz"

// ParsePasswords reads DEMO_PASSWORDS ("key=password,..."): one distinct, valid password
// for every account and nothing else.
func ParsePasswords(s string) (map[string]string, error) {
	out := map[string]string{}
	seen := map[string]bool{}
	for i, pair := range strings.Split(s, ",") {
		key, pw, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok { // the position only: the entry may hold a password
			return nil, fmt.Errorf("DEMO_PASSWORDS: entry %d is not key=password", i+1)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("DEMO_PASSWORDS: %s appears twice", key)
		}
		if err := auth.ValidatePassword(pw); err != nil {
			return nil, fmt.Errorf("DEMO_PASSWORDS %s: %w", key, err)
		}
		if seen[pw] {
			return nil, fmt.Errorf("DEMO_PASSWORDS: %s shares a password", key)
		}
		seen[pw], out[key] = true, pw
	}
	for _, a := range Accounts {
		if out[a.Key] == "" {
			return nil, fmt.Errorf("DEMO_PASSWORDS: no password for %s", a.Key)
		}
	}
	if len(out) != len(Accounts) {
		return nil, errors.New("DEMO_PASSWORDS: unknown account")
	}
	return out, nil
}

// Services are the real services the seed acts through.
type Services struct {
	Pool     *pgxpool.Pool
	Auth     *auth.Service
	People   *people.Service
	Catalog  *catalog.Service
	Requests *requests.Service
	Access   *access.Service
	Reviews  *reviews.Service
	Tools    access.ToolClient
	Location *time.Location
}

type person struct {
	local, name, department, title, manager string // manager: its local part, created before
	roles                                   []string
}

// The company; managers come before their reports. The initial Super Admin ("admin") exists.
var company = []person{
	{"selin.aydin", "Selin Aydın", "Bilgi Güvenliği", "Bilgi Güvenliği Uzmanı", "admin", []string{"INFOSEC"}},
	{"emre.kaya", "Emre Kaya", "Bilgi Güvenliği", "Bulut Güvenlik Mühendisi", "admin", []string{"INFOSEC"}},
	{"ayse.demir", "Ayşe Demir", "İnsan Kaynakları", "İK Yöneticisi", "admin", []string{"HR_MANAGER"}},
	{"gizem.arslan", "Gizem Arslan", "İç Denetim", "İç Denetçi", "admin", []string{"AUDITOR"}},
	{"can.ozturk", "Can Öztürk", "Bilgi Teknolojileri", "BT Operasyon Lideri", "admin", nil},
	{"burak.sahin", "Burak Şahin", "Ürün Geliştirme", "Mühendislik Yöneticisi", "admin", nil},
	{"deniz.kara", "Deniz Kara", "Ürün Geliştirme", "Yazılım Geliştirici", "burak.sahin", nil},
	{"elif.celik", "Elif Çelik", "Ürün Geliştirme", "Kıdemli Yazılım Geliştirici", "burak.sahin", nil},
	{"mert.acar", "Mert Acar", "Ürün Geliştirme", "DevOps Mühendisi", "burak.sahin", nil},
	{"zeynep.yildiz", "Zeynep Yıldız", "Ürün Geliştirme", "Ürün Tasarımcısı", "burak.sahin", nil},
	{"hakan.koc", "Hakan Koç", "Finans", "Finans Müdürü", "admin", nil},
	{"ece.polat", "Ece Polat", "Finans", "Bordro Uzmanı", "hakan.koc", nil},
	{"levent.uysal", "Levent Uysal", "Finans", "Muhasebe Uzmanı", "hakan.koc", nil},
	{"serkan.tas", "Serkan Taş", "Satış", "Satış Müdürü", "admin", nil},
	{"leyla.ekinci", "Leyla Ekinci", "Satış", "Satış Temsilcisi", "serkan.tas", nil},
	{"oya.sezer", "Oya Sezer", "Satış", "Kurumsal Satış Temsilcisi", "serkan.tas", nil},
	{"tuna.erdem", "Tuna Erdem", "Satış", "Satış Asistanı", "serkan.tas", nil},
}

type group struct {
	name    string
	members []string
}

var groups = []group{
	{"BT Operasyon", []string{"can.ozturk", "emre.kaya"}},
	{"Tedarikçi Masası", []string{"hakan.koc", "ayse.demir"}},
}

type level struct {
	name               string
	toolOwner, infosec bool
}

type tool struct {
	name, method, owner, group string
	levels                     []level
}

// One or more tools per fulfillment method, with the F2 examples.
var tools = []tool{
	{"Jira", "API", "can.ozturk", "BT Operasyon", []level{{"Kullanıcı", false, false}, {"Yönetici", true, true}}},
	{"GitHub", "MANUAL", "can.ozturk", "BT Operasyon", []level{{"Geliştirici", true, false}}},
	{"AWS Konsolu", "API", "emre.kaya", "BT Operasyon", []level{{"Salt okunur", true, false}, {"Yönetici", true, true}}},
	{"Figma", "MANUAL", "zeynep.yildiz", "BT Operasyon", []level{{"Editör", false, false}}},
	{"Salesforce", "API", "serkan.tas", "BT Operasyon", []level{{"Satış Temsilcisi", true, false}}},
	{"Bordro Portalı", "VENDOR", "ayse.demir", "Tedarikçi Masası", []level{{"Kullanıcı", true, false}}},
}

// starter is the Ürün Geliştirme starter package.
var starter = [][2]string{{"Jira", "Kullanıcı"}, {"GitHub", "Geliştirici"}}

const domain = "@accessflow.local"

// errNotFresh tells how to recover from a database the seed cannot complete.
var errNotFresh = errors.New("demo seed: the database holds data from an interrupted start or an earlier version; " +
	"reset it with `docker compose down -v` and start again")

// Seed builds the demo company and its workflows once, on a database that holds only the
// demo Super Admin (adminEmail), and marks it done in settings. Later starts see the marker
// and do nothing. Any other database is refused rather than half-completed by guesswork.
func Seed(ctx context.Context, svc Services, adminEmail string, passwords map[string]string) error {
	var done, fresh bool
	if err := svc.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM settings WHERE key = 'demo_seed'),
		NOT EXISTS (SELECT 1 FROM employees WHERE email <> $1) AND NOT EXISTS (SELECT 1 FROM tools)
		AND EXISTS (SELECT 1 FROM employees e JOIN password_credentials c ON c.employee_id = e.id
		            WHERE e.email = $1 AND NOT c.is_temporary)`, adminEmail).Scan(&done, &fresh); err != nil {
		return err
	}
	if done {
		return nil
	}
	if !fresh {
		return errNotFresh
	}
	s := &seeder{Services: svc, ids: map[string]uuid.UUID{}, levels: map[string]uuid.UUID{}, toolIDs: map[string]uuid.UUID{}}
	if err := s.people(ctx, adminEmail, passwords); err != nil {
		return err
	}
	if err := s.catalog(ctx); err != nil {
		return err
	}
	if err := s.workflows(ctx); err != nil {
		return fmt.Errorf("demo workflows: %w", err)
	}
	if _, err := svc.Pool.Exec(ctx, "INSERT INTO settings (key, value) VALUES ('demo_seed', 'complete')"); err != nil {
		return err
	}
	slog.Info("demo seed complete")
	return nil
}

type seeder struct {
	Services
	ids     map[string]uuid.UUID // by local part; "admin" is the Super Admin
	toolIDs map[string]uuid.UUID
	levels  map[string]uuid.UUID // "Tool/Level"
}

func (s *seeder) people(ctx context.Context, adminEmail string, passwords map[string]string) error {
	var admin uuid.UUID
	if err := s.Pool.QueryRow(ctx, "SELECT id FROM employees WHERE email = $1", adminEmail).Scan(&admin); err != nil {
		return fmt.Errorf("demo: initial Super Admin %s: %w", adminEmail, err)
	}
	s.ids["admin"] = admin
	known := map[string]string{} // demo account email → password
	for _, a := range Accounts {
		known[a.Email] = passwords[a.Key]
	}
	for _, p := range company {
		email := p.local + domain
		password := known[email]
		if password == "" { // not a demo account: a random permanent password nobody knows
			var err error
			if password, err = auth.NewTemporaryPassword(); err != nil {
				return err
			}
		}
		manager := s.ids[p.manager]
		e, _, err := s.People.CreateEmployee(ctx, people.SystemActor, people.NewEmployee{Email: email, FullName: p.name,
			Department: &p.department, JobTitle: &p.title, ManagerID: &manager, SeedPassword: password})
		if err != nil {
			return fmt.Errorf("create %s: %w", email, err)
		}
		s.ids[p.local] = e.ID
		for _, role := range p.roles {
			if err := s.People.AssignRole(ctx, people.SystemActor, e.ID, role); err != nil {
				return fmt.Errorf("assign %s to %s: %w", role, email, err)
			}
		}
	}
	return nil
}

func (s *seeder) catalog(ctx context.Context) error {
	groupIDs := map[string]uuid.UUID{}
	for _, g := range groups {
		made, err := s.People.CreateGroup(ctx, people.SystemActor, g.name)
		if err != nil {
			return fmt.Errorf("create group %s: %w", g.name, err)
		}
		groupIDs[g.name] = made.ID
		for _, m := range g.members {
			if err := s.People.AddMember(ctx, people.SystemActor, made.ID, s.ids[m]); err != nil {
				return fmt.Errorf("add %s to %s: %w", m, g.name, err)
			}
		}
	}
	for _, t := range tools {
		made, err := s.Catalog.CreateTool(ctx, people.SystemActor, catalog.NewTool{Name: t.name, FulfillmentMethod: t.method,
			OwnerID: s.ids[t.owner], AdminGroupID: groupIDs[t.group]})
		if err != nil {
			return fmt.Errorf("create tool %s: %w", t.name, err)
		}
		s.toolIDs[t.name] = made.ID
		for _, l := range t.levels {
			lvl, err := s.Catalog.CreateLevel(ctx, people.SystemActor, made.ID, catalog.NewLevel{Name: l.name,
				RequiresToolOwner: l.toolOwner, RequiresInfosec: l.infosec})
			if err != nil {
				return fmt.Errorf("create level %s/%s: %w", t.name, l.name, err)
			}
			s.levels[t.name+"/"+l.name] = lvl.ID
			for _, p := range starter {
				if p == [2]string{t.name, l.name} {
					if _, err := s.Catalog.AddPackage(ctx, people.SystemActor, catalog.NewPackage{Department: "Ürün Geliştirme", AccessLevelID: lvl.ID}); err != nil {
						return fmt.Errorf("starter package %s/%s: %w", t.name, l.name, err)
					}
				}
			}
		}
	}
	return nil
}
