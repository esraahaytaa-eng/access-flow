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
	Role  string `json:"role"` // the role it demonstrates (English, like the demo sign-in labels)
}

// Accounts are the documented demo sign-ins, one per persona. The Super Admin is the
// initial Super Admin (auth.Bootstrap).
var Accounts = []Account{
	{"admin", "admin@accessflow.local", "Super Admin"},
	{"infosec", "selin.aydin@accessflow.local", "Information Security"},
	{"hr", "ayse.demir@accessflow.local", "HR Manager"},
	{"auditor", "gizem.arslan@accessflow.local", "Auditor"},
	{"manager", "burak.sahin@accessflow.local", "Manager"},
	{"owner", "can.ozturk@accessflow.local", "Tool Owner & IT Operations"},
	{"cloud", "emre.kaya@accessflow.local", "Cloud Tool Owner & InfoSec"},
	{"employee", "deniz.kara@accessflow.local", "Employee"},
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
	{"selin.aydin", "Selin Aydın", "Information Security", "Security Analyst", "admin", []string{"INFOSEC"}},
	{"emre.kaya", "Emre Kaya", "Information Security", "Cloud Security Engineer", "admin", []string{"INFOSEC"}},
	{"ayse.demir", "Ayşe Demir", "Human Resources", "HR Manager", "admin", []string{"HR_MANAGER"}},
	{"gizem.arslan", "Gizem Arslan", "Compliance", "Auditor", "admin", []string{"AUDITOR"}},
	{"can.ozturk", "Can Öztürk", "IT Operations", "IT Operations Lead", "admin", nil},
	{"burak.sahin", "Burak Şahin", "Engineering", "Engineering Manager", "admin", nil},
	{"deniz.kara", "Deniz Kara", "Engineering", "Backend Developer", "burak.sahin", nil},
	{"elif.celik", "Elif Çelik", "Engineering", "Senior Backend Developer", "burak.sahin", nil},
	{"mert.acar", "Mert Acar", "Engineering", "DevOps Engineer", "burak.sahin", nil},
	{"zeynep.yildiz", "Zeynep Yıldız", "Engineering", "Product Designer", "burak.sahin", nil},
	{"hakan.koc", "Hakan Koç", "Finance", "Finance Manager", "admin", nil},
	{"ece.polat", "Ece Polat", "Finance", "Payroll Specialist", "hakan.koc", nil},
	{"levent.uysal", "Levent Uysal", "Finance", "Accountant", "hakan.koc", nil},
	{"serkan.tas", "Serkan Taş", "Sales", "Sales Manager", "admin", nil},
	{"leyla.ekinci", "Leyla Ekinci", "Sales", "Sales Representative", "serkan.tas", nil},
	{"oya.sezer", "Oya Sezer", "Sales", "Enterprise Account Executive", "serkan.tas", nil},
	{"tuna.erdem", "Tuna Erdem", "Sales", "Sales Assistant", "serkan.tas", nil},
}

// The company catalog (M17): English names, as a company defines them; the UI stays Turkish.
var (
	departments = []string{"Engineering", "Human Resources", "Information Security", "Compliance", "Finance", "Sales",
		"IT Operations", "Management"}
	positions = []string{"Backend Developer", "Senior Backend Developer", "Frontend Developer", "DevOps Engineer",
		"Product Designer", "Engineering Manager", "HR Manager", "Security Analyst", "Cloud Security Engineer", "Auditor",
		"IT Operations Lead", "Finance Manager", "Payroll Specialist", "Accountant", "Sales Manager", "Sales Representative",
		"Enterprise Account Executive", "Sales Assistant"}
)

type group struct {
	name    string
	members []string
}

var groups = []group{
	{"IT Operations", []string{"can.ozturk", "emre.kaya"}},
	{"Vendor Desk", []string{"hakan.koc", "ayse.demir"}},
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
	{"Jira", "API", "can.ozturk", "IT Operations", []level{{"User", false, false}, {"Admin", true, true}}},
	{"GitHub", "MANUAL", "can.ozturk", "IT Operations", []level{{"Developer", true, false}}},
	{"AWS Console", "API", "emre.kaya", "IT Operations", []level{{"ReadOnly", true, false}, {"Admin", true, true}}},
	{"Figma", "MANUAL", "zeynep.yildiz", "IT Operations", []level{{"Editor", false, false}}},
	{"Salesforce", "API", "serkan.tas", "IT Operations", []level{{"Sales User", true, false}}},
	{"Payroll Portal", "VENDOR", "ayse.demir", "Vendor Desk", []level{{"User", true, false}}},
}

// starter is the Engineering starter package.
var starter = [][2]string{{"Jira", "User"}, {"GitHub", "Developer"}}

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
	for list, names := range map[string][]string{"departments": departments, "job-positions": positions} {
		for _, n := range names {
			if _, err := s.People.AddEntry(ctx, people.SystemActor, list, n); err != nil {
				return fmt.Errorf("define %s %s: %w", list, n, err)
			}
		}
	}
	known := map[string]string{} // demo account email → password
	for _, a := range Accounts {
		known[a.Email] = passwords[a.Key]
	}
	for _, p := range company {
		email := p.local + domain
		manager := s.ids[p.manager]
		// Other employees get no credential and no link (M18); a Super Admin can send one.
		e, err := s.People.CreateEmployee(ctx, people.SystemActor, people.NewEmployee{Email: email, FullName: p.name,
			Department: &p.department, JobTitle: &p.title, ManagerID: &manager, Seed: true, SeedPassword: known[email]})
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
					if _, err := s.Catalog.AddPackage(ctx, people.SystemActor, catalog.NewPackage{Department: "Engineering", AccessLevelID: lvl.ID}); err != nil {
						return fmt.Errorf("starter package %s/%s: %w", t.name, l.name, err)
					}
				}
			}
		}
	}
	return nil
}
