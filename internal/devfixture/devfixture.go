// Package devfixture creates a small set of employees, admin groups and tools for trying
// AccessFlow by hand (M3, M4, D12). It runs only when invoked (`accessflow dev-fixture`), goes through the real
// services as the system, and is idempotent. The full demo seed is M15.
package devfixture

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/catalog"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
)

type person struct {
	email, name, department, title, role, manager string
}

// Managers come before their reports.
var fixture = []person{
	{"hr@accessflow.local", "Hande Recruiter", "People", "HR Manager", "HR_MANAGER", ""},
	{"infosec1@accessflow.local", "Ilker Security", "Security", "Security Engineer", "INFOSEC", ""},
	{"infosec2@accessflow.local", "Irem Security", "Security", "Security Engineer", "INFOSEC", ""},
	{"auditor@accessflow.local", "Aylin Auditor", "Internal Audit", "Auditor", "AUDITOR", ""},
	{"manager@accessflow.local", "Mert Manager", "Engineering", "Engineering Manager", "", ""},
	{"dev1@accessflow.local", "Deniz Developer", "Engineering", "Developer", "", "manager@accessflow.local"},
	{"dev2@accessflow.local", "Derya Developer", "Engineering", "Developer", "", "manager@accessflow.local"},
}

type group struct {
	name    string
	members []string
}

var groups = []group{
	{"IT Operations", []string{"infosec1@accessflow.local", "manager@accessflow.local"}},
	{"Vendor Desk", []string{"hr@accessflow.local"}},
}

type level struct {
	name               string
	toolOwner, infosec bool
	starter            bool // in the Engineering starter package
}

type tool struct {
	name, method, owner, group string
	levels                     []level
}

// One tool per fulfillment method, and the F2 examples: Jira Viewer, GitHub Developer, AWS Admin.
var tools = []tool{
	{"Jira", "API", "manager@accessflow.local", "IT Operations", []level{{"Viewer", false, false, true}, {"Admin", true, true, false}}},
	{"GitHub", "MANUAL", "manager@accessflow.local", "IT Operations", []level{{"Developer", true, false, true}}},
	{"AWS", "API", "infosec2@accessflow.local", "IT Operations", []level{{"Admin", true, true, false}}},
	{"Payroll Portal", "VENDOR", "hr@accessflow.local", "Vendor Desk", []level{{"User", true, false, false}}},
}

// Run creates whatever fixture employees, admin groups and tools are missing, and returns
// what it created. The employees share password (permanent, seed-marked). Existing records
// are left exactly as they are, so a re-run never undoes an administrator's change.
func Run(ctx context.Context, svc *people.Service, cat *catalog.Service, password string) ([]string, error) {
	if err := auth.ValidatePassword(password); err != nil {
		return nil, fmt.Errorf("SEED_PASSWORD: %w", err)
	}
	list, err := svc.ListEmployees(ctx, people.SystemActor)
	if err != nil {
		return nil, err
	}
	ids := map[string]uuid.UUID{}
	for _, e := range list {
		ids[e.Email] = e.ID
	}

	var created []string
	for _, p := range fixture {
		if _, exists := ids[p.email]; exists {
			continue
		}
		{
			in := people.NewEmployee{Email: p.email, FullName: p.name, Department: &p.department, JobTitle: &p.title, SeedPassword: password}
			if p.manager != "" {
				m := ids[p.manager]
				in.ManagerID = &m
			}
			e, _, err := svc.CreateEmployee(ctx, people.SystemActor, in)
			if err != nil {
				return created, fmt.Errorf("create %s: %w", p.email, err)
			}
			ids[p.email] = e.ID
			created = append(created, p.email)
		}
		if p.role != "" {
			if err := svc.AssignRole(ctx, people.SystemActor, ids[p.email], p.role); err != nil {
				return created, fmt.Errorf("assign %s to %s: %w", p.role, p.email, err)
			}
		}
	}
	return createCatalog(ctx, svc, cat, ids, created)
}

func createCatalog(ctx context.Context, svc *people.Service, cat *catalog.Service, ids map[string]uuid.UUID, created []string) ([]string, error) {
	existingGroups, err := svc.ListGroups(ctx, people.SystemActor)
	if err != nil {
		return created, err
	}
	groupIDs := map[string]uuid.UUID{}
	for _, g := range existingGroups {
		groupIDs[strings.ToLower(g.Name)] = g.ID // names are unique ignoring case
	}
	for _, g := range groups {
		if _, exists := groupIDs[strings.ToLower(g.name)]; exists {
			continue
		}
		made, err := svc.CreateGroup(ctx, people.SystemActor, g.name)
		if err != nil {
			return created, fmt.Errorf("create group %s: %w", g.name, err)
		}
		groupIDs[strings.ToLower(g.name)] = made.ID
		created = append(created, "admin group "+g.name)
		for _, m := range g.members {
			if err := svc.AddMember(ctx, people.SystemActor, made.ID, ids[m]); err != nil {
				return created, fmt.Errorf("add %s to %s: %w", m, g.name, err)
			}
		}
	}

	existingTools, err := cat.Catalog(ctx, people.SystemActor)
	if err != nil {
		return created, err
	}
	toolExists := map[string]bool{}
	for _, t := range existingTools {
		toolExists[strings.ToLower(t.Name)] = true
	}
	for _, t := range tools {
		if toolExists[strings.ToLower(t.name)] {
			continue
		}
		made, err := cat.CreateTool(ctx, people.SystemActor, catalog.NewTool{Name: t.name, FulfillmentMethod: t.method,
			OwnerID: ids[t.owner], AdminGroupID: groupIDs[strings.ToLower(t.group)]})
		if err != nil {
			return created, fmt.Errorf("create tool %s: %w", t.name, err)
		}
		created = append(created, "tool "+t.name)
		for _, l := range t.levels {
			lvl, err := cat.CreateLevel(ctx, people.SystemActor, made.ID, catalog.NewLevel{Name: l.name,
				RequiresToolOwner: l.toolOwner, RequiresInfosec: l.infosec})
			if err != nil {
				return created, fmt.Errorf("create level %s/%s: %w", t.name, l.name, err)
			}
			if l.starter {
				if _, err := cat.AddPackage(ctx, people.SystemActor, catalog.NewPackage{Department: "Engineering", AccessLevelID: lvl.ID}); err != nil {
					return created, fmt.Errorf("starter package %s/%s: %w", t.name, l.name, err)
				}
			}
		}
	}
	return created, nil
}
