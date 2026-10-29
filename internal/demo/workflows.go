package demo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esraahaytaa-eng/access-flow/internal/access"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/people"
	"github.com/esraahaytaa-eng/access-flow/internal/requests"
)

// namespace makes the seed's submission and tool operation IDs stable across runs.
var namespace = uuid.MustParse("6f1c2a6e-3c1d-4b7e-9f3a-acce55f10e00")

func stableID(name string) uuid.UUID { return uuid.NewSHA1(namespace, []byte(name)) }

// workflows plays a few weeks of ordinary work, each step by the person whose role it needs.
func (s *seeder) workflows(ctx context.Context) error {
	// Before AccessFlow: accounts already in the tools, one of a current employee and two
	// nobody owns, then the baseline import (D10).
	for _, a := range []struct{ account, level string }{
		{"elif.celik" + domain, "Jira/User"},
		{"eski.stajyer" + domain, "Jira/User"},
		{"ajans@partner-reklam.com", "Salesforce/Sales User"},
	} {
		tool, _, _ := strings.Cut(a.level, "/")
		if _, err := s.Tools.Apply(ctx, access.ToolOperation{ID: stableID("legacy/" + a.account + a.level), Type: "GRANT",
			Tool: s.toolIDs[tool].String(), Account: a.account, Level: s.levels[a.level].String()}); err != nil {
			return fmt.Errorf("legacy account %s: %w", a.account, err)
		}
	}
	admin, err := s.as(ctx, "admin")
	if err != nil {
		return err
	}
	if _, err := s.Access.ImportMock(ctx, admin); err != nil {
		return fmt.Errorf("baseline import: %w", err)
	}

	// Approved and fulfilled: API access the runner grants, Manual access a member confirms.
	deniz, err := s.request(ctx, "deniz.kara", "Sprint planlaması ve kod incelemeleri", nil, "Jira/User", "GitHub/Developer")
	if err != nil {
		return err
	}
	if err := s.approveAll(ctx, deniz...); err != nil {
		return err
	}
	if err := s.task(ctx, "deniz.kara", "GitHub", "can.ozturk", "confirm"); err != nil {
		return err
	}
	expires := time.Now().In(s.Location).AddDate(0, 0, 5).Format(time.DateOnly)
	leyla, err := s.request(ctx, "leyla.ekinci", "Çeyrek sonu müşteri kampanyası", &expires, "Salesforce/Sales User")
	if err != nil {
		return err
	}
	oya, err := s.request(ctx, "oya.sezer", "Kurumsal müşteri portföyü", nil, "Salesforce/Sales User")
	if err != nil {
		return err
	}
	levent, err := s.request(ctx, "levent.uysal", "Fatura tasarımlarının incelenmesi", nil, "Figma/Editor")
	if err != nil {
		return err
	}
	if err := s.approveAll(ctx, append(append(leyla, oya...), levent...)...); err != nil {
		return err
	}
	if err := s.task(ctx, "levent.uysal", "Figma", "can.ozturk", "confirm"); err != nil {
		return err
	}

	// A Vendor task with the vendor's ticket, and a Manual grant the admin could not apply.
	ece, err := s.request(ctx, "ece.polat", "Aylık bordro kontrolü", nil, "Payroll Portal/User")
	if err != nil {
		return err
	}
	mert, err := s.request(ctx, "mert.acar", "Altyapı kodu depoları", nil, "GitHub/Developer")
	if err != nil {
		return err
	}
	if err := s.approveAll(ctx, append(ece, mert...)...); err != nil {
		return err
	}
	if err := s.task(ctx, "ece.polat", "Payroll Portal", "hakan.koc", "ticket"); err != nil {
		return err
	}
	if err := s.task(ctx, "mert.acar", "GitHub", "emre.kaya", "fail"); err != nil {
		return err
	}

	// Waiting: at the manager, at the tool owner, at InfoSec.
	if _, err := s.request(ctx, "mert.acar", "Üretim ortamı olay müdahalesi", nil, "AWS Console/Admin"); err != nil {
		return err
	}
	elif, err := s.request(ctx, "elif.celik", "Mobil uygulama deposu", nil, "GitHub/Developer")
	if err != nil {
		return err
	}
	if err := s.approveSteps(ctx, 1, elif...); err != nil {
		return err
	}
	zeynep, err := s.request(ctx, "zeynep.yildiz", "Tasarım sistemi panosunun yönetimi", nil, "Jira/Admin")
	if err != nil {
		return err
	}
	if err := s.approveSteps(ctx, 2, zeynep...); err != nil {
		return err
	}

	// Rejected and withdrawn.
	tuna, err := s.request(ctx, "tuna.erdem", "Raporlara göz atmak", nil, "AWS Console/ReadOnly")
	if err != nil {
		return err
	}
	if err := s.reject(ctx, tuna[0], "Satış raporları için AWS erişimi gerekmiyor; BI panosunu kullanın."); err != nil {
		return err
	}
	hakan, err := s.request(ctx, "hakan.koc", "Bütçe takibi", nil, "Jira/User")
	if err != nil {
		return err
	}
	if _, err := s.Requests.Withdraw(ctx, s.must(ctx, "hakan.koc"), hakan[0].ID, hakan[0].Version, "Finans ekibi Jira yerine mevcut aracı kullanacak."); err != nil {
		return fmt.Errorf("withdraw: %w", err)
	}

	// A Super Admin's direct grant (D1): visible in the overrides report.
	until := time.Now().In(s.Location).AddDate(0, 0, 14).Format(time.DateOnly)
	if _, _, err := s.Requests.DirectGrant(ctx, admin, requests.DirectGrantInput{SubmissionID: stableID("direct/elif"), BeneficiaryID: s.ids["elif.celik"],
		AccessLevelID: s.levels["Figma/Editor"], Justification: "Lansman öncesi acil tasarım düzeltmesi", ExpiresOn: &until}); err != nil {
		return fmt.Errorf("direct grant: %w", err)
	}

	// People changes, once their API access is active (D7).
	if err := s.settle(ctx); err != nil {
		return err
	}
	hr := s.must(ctx, "ayse.demir")
	if err := s.lifecycle(ctx, "oya.sezer", func(e people.Employee) error {
		_, err := s.Requests.Suspend(ctx, hr, e.ID, e.Version, "Uzun süreli izin")
		return err
	}); err != nil {
		return err
	}
	if err := s.lifecycle(ctx, "levent.uysal", func(e people.Employee) error {
		_, err := s.Requests.Terminate(ctx, hr, e.ID, e.Version, "İstifa")
		return err
	}); err != nil {
		return err
	}
	if err := s.lifecycle(ctx, "tuna.erdem", func(e people.Employee) error {
		_, err := s.Requests.ScheduleTermination(ctx, hr, e.ID, e.Version, time.Now().AddDate(0, 0, 21), "Sözleşme sonu")
		return err
	}); err != nil {
		return err
	}

	// The quarterly review, started by hand (D8), with a first decision by the tool owner (the reviewer).
	if err := s.settle(ctx); err != nil {
		return err
	}
	if _, err := s.Reviews.StartCampaign(ctx, admin); err != nil {
		return fmt.Errorf("review campaign: %w", err)
	}
	var itemID uuid.UUID
	var version int
	if err := s.Pool.QueryRow(ctx, `SELECT i.id, i.version FROM review_items i JOIN grants g ON g.id = i.grant_id
		WHERE g.employee_id = $1 AND g.tool_id = $2`, s.ids["deniz.kara"], s.toolIDs["Jira"]).Scan(&itemID, &version); err != nil {
		return fmt.Errorf("review item: %w", err)
	}
	if _, err := s.Reviews.Decide(ctx, s.must(ctx, "can.ozturk"), itemID, "KEEP", "", version); err != nil {
		return fmt.Errorf("review decision: %w", err)
	}
	return nil
}

// as is the principal of a seeded person, as a session would load it.
func (s *seeder) as(ctx context.Context, local string) (auth.Principal, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return auth.Principal{}, err
	}
	defer tx.Rollback(ctx)
	e, err := people.GetEmployee(ctx, tx, s.ids[local])
	if err != nil {
		return auth.Principal{}, fmt.Errorf("%s: %w", local, err)
	}
	return auth.Principal{EmployeeID: e.ID, Email: e.Email, FullName: e.FullName, Roles: e.Roles, AdminGroups: e.AdminGroups}, nil
}

// must is as for a person the seed created moments ago; a failure is logged and surfaces at the call it feeds.
func (s *seeder) must(ctx context.Context, local string) auth.Principal {
	p, err := s.as(ctx, local)
	if err != nil { // the action that follows fails too; this names the cause
		slog.Error("demo seed: load actor", "err", err)
	}
	return p
}

// request submits a NEW_ACCESS request as the beneficiary.
func (s *seeder) request(ctx context.Context, who, why string, expiresOn *string, levels ...string) ([]requests.Item, error) {
	in := requests.Submission{SubmissionID: stableID("request/" + who + levels[0])}
	for _, l := range levels {
		d := requests.Duration{Type: "UNTIL_REVOKED"}
		if expiresOn != nil {
			d = requests.Duration{Type: "TEMPORARY", ExpiresOn: expiresOn}
		}
		in.Items = append(in.Items, requests.ItemInput{AccessLevelID: s.levels[l], Justification: why, Duration: d})
	}
	r, _, err := s.Requests.Submit(ctx, s.must(ctx, who), in)
	if err != nil {
		return nil, fmt.Errorf("request %s %v: %w", who, levels, err)
	}
	return r.Items, nil
}

// openStep is an item's open approval step and who may decide it: its assignee, or the
// InfoSec pool.
func (s *seeder) openStep(ctx context.Context, item uuid.UUID) (step uuid.UUID, version int, deciders []string, err error) {
	var assignee *uuid.UUID
	err = s.Pool.QueryRow(ctx, `SELECT id, version, assignee_id FROM approval_steps WHERE item_id = $1 AND status = 'PENDING'`,
		item).Scan(&step, &version, &assignee)
	if err != nil {
		return
	}
	if assignee == nil {
		return step, version, []string{"selin.aydin", "emre.kaya"}, nil
	}
	for local, id := range s.ids {
		if id == *assignee {
			return step, version, []string{local}, nil
		}
	}
	return step, version, nil, fmt.Errorf("step %s: assignee not seeded", step)
}

// approveSteps approves up to n open steps of each item, each by the person it is routed to.
func (s *seeder) approveSteps(ctx context.Context, n int, items ...requests.Item) error {
	for _, it := range items {
		for range n {
			step, version, deciders, err := s.openStep(ctx, it.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				break // fully approved
			}
			if err != nil {
				return err
			}
			for _, d := range deciders { // the first InfoSec member may be excluded (F2): try the next
				if _, err = s.Requests.Approve(ctx, s.must(ctx, d), step, version); err == nil {
					break
				}
			}
			if err != nil {
				return fmt.Errorf("approve %s %s: %w", it.Tool, it.Level, err)
			}
		}
	}
	return nil
}

func (s *seeder) approveAll(ctx context.Context, items ...requests.Item) error {
	return s.approveSteps(ctx, 3, items...)
}

func (s *seeder) reject(ctx context.Context, it requests.Item, reason string) error {
	step, version, deciders, err := s.openStep(ctx, it.ID)
	if err != nil {
		return err
	}
	_, err = s.Requests.Reject(ctx, s.must(ctx, deciders[0]), step, version, reason)
	return err
}

// task works the beneficiary's open Manual or Vendor grant task as an admin group member.
func (s *seeder) task(ctx context.Context, beneficiary, tool, member, outcome string) error {
	actor := s.must(ctx, member)
	list, err := s.Access.Tasks(ctx, actor)
	if err != nil {
		return err
	}
	for _, t := range list {
		if t.Employee.ID != s.ids[beneficiary] || t.Tool != tool || t.Type != "GRANT" {
			continue
		}
		claimed, err := s.Access.Claim(ctx, actor, t.ID, t.Version)
		if err != nil {
			return fmt.Errorf("claim %s task: %w", tool, err)
		}
		switch outcome {
		case "confirm":
			_, err = s.Access.Confirm(ctx, actor, t.ID, claimed.Version)
		case "ticket":
			_, err = s.Access.RecordTicket(ctx, actor, t.ID, claimed.Version, "TDR-2026-0142")
		case "fail":
			_, err = s.Access.Fail(ctx, actor, t.ID, claimed.Version, "GitHub organizasyonundaki lisans sınırı doldu")
		}
		if err != nil {
			return fmt.Errorf("%s %s task: %w", outcome, tool, err)
		}
		return nil
	}
	return fmt.Errorf("no %s task for %s", tool, beneficiary)
}

func (s *seeder) lifecycle(ctx context.Context, who string, do func(people.Employee) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	e, err := people.GetEmployee(ctx, tx, s.ids[who])
	tx.Rollback(ctx)
	if err != nil {
		return err
	}
	if err := do(e); err != nil {
		return fmt.Errorf("%s: %w", who, err)
	}
	return nil
}

// settle waits until the runner has applied every API operation: suspension and the review
// snapshot act on active access only (real time; no job is forced).
func (s *seeder) settle(ctx context.Context) error {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var open bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM operations o JOIN grants g ON g.id = o.grant_id
			WHERE g.method = 'API' AND o.status IN ('PENDING', 'IN_PROGRESS'))`).Scan(&open); err != nil {
			return err
		}
		if !open {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the tools did not finish in 2 minutes")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}
