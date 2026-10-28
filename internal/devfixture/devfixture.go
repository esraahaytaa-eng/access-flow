// Package devfixture creates a small set of employees for trying AccessFlow by hand (M3,
// D12). It runs only when invoked (`accessflow dev-fixture`), goes through the real
// services as the system, and is idempotent. The full demo seed is M15.
package devfixture

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/auth"
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

// Run creates the missing fixture employees with their roles, all sharing password
// (permanent, seed-marked), and returns the emails it created. Existing employees are left
// exactly as they are, so a re-run never undoes an administrator's change.
func Run(ctx context.Context, svc *people.Service, password string) ([]string, error) {
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
	return created, nil
}
