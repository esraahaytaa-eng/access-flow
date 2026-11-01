package access

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/esraahaytaa-eng/access-flow/internal/audit"
	"github.com/esraahaytaa-eng/access-flow/internal/auth"
	"github.com/esraahaytaa-eng/access-flow/internal/platform"
)

// ImportLockKey makes imports run one at a time: each checks what was imported before.
const ImportLockKey int64 = 0x0D10_1A70

var (
	errInvalidCSV      = platform.ValidationError("content", "invalid_csv")
	errToolUnavailable = platform.NewError(http.StatusBadGateway, "tool_unavailable")
	errOrphanClosed    = platform.NewError(http.StatusConflict, "orphan_closed")
)

// ImportReport is what one baseline import did (D10).
type ImportReport struct {
	ID         uuid.UUID  `json:"id"`
	Source     string     `json:"source"` // MOCK or CSV
	FileName   *string    `json:"fileName"`
	ImportedBy Person     `json:"importedBy"`
	ImportedAt time.Time  `json:"importedAt"`
	Imported   int        `json:"imported"` // new grants, orphans included
	Orphans    int        `json:"orphans"`
	Terminated int        `json:"terminated"` // accounts of terminated employees, removal started
	Skipped    int        `json:"skipped"`    // imported before, or already granted in AccessFlow
	Rejected   []Rejected `json:"rejected"`
	// Tool read only: reported grants it found in the tool, and reported API grants of the
	// tools it read that the tool does not hold (M17).
	Verified   *int `json:"verified,omitempty"`
	Unverified *int `json:"unverified,omitempty"`
	// Workbook only: the employees, tools and levels it created (M17).
	Created map[string]int `json:"created,omitempty"`
}

// Rejected is a row the import could not place: unknown_tool, unknown_level, api_tool
// (read from the tool itself) or invalid_row.
type Rejected struct {
	Line    int    `json:"line,omitempty"` // CSV only
	Account string `json:"account"`
	Tool    string `json:"tool"`
	Level   string `json:"level"`
	Reason  string `json:"reason"`
}

// Holding is one (tool, account, level) to import; Email matches the employee.
type Holding struct {
	Email, Account string
	Tool, Level    uuid.UUID
	Method         string
}

// ImportMock imports the accounts the Mock Tool Service holds for every active API tool.
func (s *Service) ImportMock(ctx context.Context, actor auth.Principal) (ImportReport, error) {
	if !actor.HasRole("SUPER_ADMIN") {
		return ImportReport{}, platform.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT t.id, t.name, l.id FROM tools t JOIN access_levels l ON l.tool_id = t.id
		WHERE t.fulfillment_method = 'API' AND t.status = 'ACTIVE' AND t.account_kind IS NULL ORDER BY t.name, t.id`) // GitHub tools are not in the mock
	if err != nil {
		return ImportReport{}, err
	}
	type toolLevel struct {
		Tool  uuid.UUID
		Name  string
		Level uuid.UUID
	}
	pairs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[toolLevel])
	if err != nil {
		return ImportReport{}, err
	}
	var holdings []Holding
	rejected := []Rejected{}
	for i, p := range pairs {
		if i > 0 && pairs[i-1].Tool == p.Tool {
			continue
		}
		accounts, err := s.tools.Accounts(ctx, p.Tool.String())
		if err != nil {
			return ImportReport{}, errors.Join(errToolUnavailable, err)
		}
		for _, a := range accounts {
			for _, l := range a.Levels {
				level, err := uuid.Parse(l.Level)
				known := err == nil && slices.ContainsFunc(pairs, func(x toolLevel) bool { return x.Tool == p.Tool && x.Level == level })
				if !known {
					rejected = append(rejected, Rejected{Account: a.Account, Tool: p.Name, Level: l.Level, Reason: "unknown_level"})
					continue
				}
				holdings = append(holdings, Holding{a.Account, a.Account, p.Tool, level, "API"})
			}
		}
	}
	return s.importHoldings(ctx, actor, "MOCK", nil, holdings, rejected)
}

// csvHeader is the import file's header row (Turkish, as the UI): email, tool, level, account.
var csvHeader = []string{"eposta", "arac", "seviye", "hesap"}

// ImportCSV imports a semicolon-separated file of Manual and Vendor tool accounts. An empty
// account column means the account is the email address.
func (s *Service) ImportCSV(ctx context.Context, actor auth.Principal, fileName, content string) (ImportReport, error) {
	if !actor.HasRole("SUPER_ADMIN") {
		return ImportReport{}, platform.ErrForbidden
	}
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return ImportReport{}, platform.ValidationError("fileName", "field_required")
	}
	if err := platform.CheckText(fileName, "fileName", 200); err != nil {
		return ImportReport{}, err
	}
	r := csv.NewReader(strings.NewReader(content))
	r.Comma, r.FieldsPerRecord, r.TrimLeadingSpace = ';', -1, true
	header, err := r.Read()
	if err != nil || len(header) != len(csvHeader) {
		return ImportReport{}, errInvalidCSV
	}
	for i, h := range header {
		if strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))) != csvHeader[i] {
			return ImportReport{}, errInvalidCSV
		}
	}
	type level struct {
		Tool   uuid.UUID
		ToolN  string
		Method string
		Level  *uuid.UUID
		LevelN *string
	}
	rows, err := s.pool.Query(ctx, `SELECT t.id, lower(t.name), t.fulfillment_method, l.id, lower(l.name)
		FROM tools t LEFT JOIN access_levels l ON l.tool_id = t.id WHERE t.status = 'ACTIVE'`)
	if err != nil {
		return ImportReport{}, err
	}
	catalog, err := pgx.CollectRows(rows, pgx.RowToStructByPos[level])
	if err != nil {
		return ImportReport{}, err
	}
	var holdings []Holding
	rejected := []Rejected{}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ImportReport{}, errInvalidCSV // a broken quote: nothing after it can be trusted
		}
		line, _ := r.FieldPos(0)
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		for i := range rec {
			rec[i] = strings.TrimSpace(rec[i])
		}
		if len(rec) != len(csvHeader) || rec[0] == "" || rec[1] == "" || rec[2] == "" || platform.CheckText(strings.Join(rec, " "), "row", 1000) != nil {
			rejected = append(rejected, Rejected{Line: line, Account: strings.Join(rec, ";"), Reason: "invalid_row"})
			continue
		}
		email, toolName, levelName, account := rec[0], rec[1], rec[2], rec[3]
		if account == "" {
			account = email
		}
		reject := func(reason string) {
			rejected = append(rejected, Rejected{Line: line, Account: account, Tool: toolName, Level: levelName, Reason: reason})
		}
		i := slices.IndexFunc(catalog, func(c level) bool { return c.ToolN == strings.ToLower(toolName) })
		switch {
		case i < 0:
			reject("unknown_tool")
			continue
		case catalog[i].Method == "API":
			reject("api_tool")
			continue
		}
		j := slices.IndexFunc(catalog, func(c level) bool {
			return c.Tool == catalog[i].Tool && c.LevelN != nil && *c.LevelN == strings.ToLower(levelName)
		})
		if j < 0 {
			reject("unknown_level")
			continue
		}
		holdings = append(holdings, Holding{email, account, catalog[j].Tool, *catalog[j].Level, catalog[j].Method})
	}
	if len(holdings) == 0 && len(rejected) == 0 {
		return ImportReport{}, errInvalidCSV
	}
	return s.importHoldings(ctx, actor, "CSV", &fileName, holdings, rejected)
}

// importHoldings records the holdings and the import report in one transaction (D10),
// one import at a time.
func (s *Service) importHoldings(ctx context.Context, actor auth.Principal, source string, fileName *string,
	holdings []Holding, rejected []Rejected) (ImportReport, error) {
	report := ImportReport{ID: uuid.Must(uuid.NewV7()), Source: source, FileName: fileName,
		ImportedBy: Person{actor.EmployeeID, actor.FullName}, Rejected: rejected}
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		// fn may run again (platform.InTx): every count starts from zero on each run.
		report.ImportedAt, report.Imported, report.Orphans, report.Terminated, report.Skipped = now, 0, 0, 0, 0
		report.Verified, report.Unverified = nil, nil
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", ImportLockKey); err != nil {
			return err
		}
		if err := RecordHoldings(ctx, tx, now, actor, &report, holdings); err != nil {
			return err
		}
		if source == "MOCK" {
			// Reported API access of the tools read that the tool does not hold.
			var unverified int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM grants g JOIN tools t ON t.id = g.tool_id
				WHERE g.import_id IS NOT NULL AND g.tool_verified_at IS NULL AND g.status IN ('ACTIVE', 'SUSPENDED')
				  AND t.fulfillment_method = 'API' AND t.status = 'ACTIVE'`).Scan(&unverified); err != nil {
				return err
			}
			report.Unverified = &unverified
		}
		return SaveReport(ctx, tx, actor, &report)
	})
	return report, err
}

// RecordHoldings records each new holding as an IMPORTED_BASELINE grant, in the caller's
// transaction, which holds ImportLockKey (D10). A holding imported before, or already
// granted through AccessFlow, is skipped. No approval or activation date is invented, and
// no operation is sent: the tool already holds the access. A terminated employee's account
// is removed at once and a suspended employee's suspended; an account matching no employee
// is an orphan for InfoSec. A tool read (source MOCK) also verifies the grants it records
// and the reported ones it finds (M17).
func RecordHoldings(ctx context.Context, tx pgx.Tx, now time.Time, actor auth.Principal, report *ImportReport, holdings []Holding) error {
	read := report.Source == "MOCK"
	if read && report.Verified == nil {
		report.Verified = new(int)
	}
	var verifiedAt *time.Time
	if read {
		verifiedAt = &now
	}
	for _, h := range holdings {
		var employee *uuid.UUID
		var status string
		// Emails are stored lowercase (CHECK), so the unique email index applies.
		err := tx.QueryRow(ctx, "SELECT id, status FROM employees WHERE email = lower($1) FOR UPDATE", h.Email).Scan(&employee, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			employee, err = nil, nil // an orphan account
		}
		if err != nil {
			return err
		}
		var imported *uuid.UUID
		var verified, held bool
		if err := tx.QueryRow(ctx, `SELECT id, tool_verified_at IS NOT NULL OR status NOT IN ('ACTIVE', 'SUSPENDED') FROM grants
			WHERE import_id IS NOT NULL AND tool_id = $1 AND lower(account) = lower($2) AND access_level_id = $3`,
			h.Tool, h.Account, h.Level).Scan(&imported, &verified); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if imported == nil {
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM grants WHERE employee_id = $1 AND access_level_id = $2
				AND status NOT IN ('REVOKED', 'CANCELLED'))`, employee, h.Level).Scan(&held); err != nil {
				return err
			}
		}
		if imported != nil || held {
			report.Skipped++
			if read && imported != nil && !verified {
				if err := verifyGrant(ctx, tx, now, actor, *imported); err != nil {
					return err
				}
				*report.Verified++
			}
			continue
		}
		var grant uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO grants (employee_id, tool_id, access_level_id, method, status, import_id, account, created_at, tool_verified_at)
			VALUES ($1, $2, $3, $4, 'ACTIVE', $5, $6, $7, $8) RETURNING id`,
			employee, h.Tool, h.Level, h.Method, report.ID, h.Account, now, verifiedAt).Scan(&grant); err != nil {
			return err
		}
		report.Imported++
		if err := audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "GRANT_IMPORTED", Entity: "grant", EntityID: &grant,
			SubjectID: employee, Details: map[string]any{"importId": report.ID, "source": report.Source, "account": h.Account,
				"approver": "unknown: existed before AccessFlow", "orphan": employee == nil, "toolVerified": read}}); err != nil {
			return err
		}
		switch {
		case employee == nil:
			report.Orphans++
		case status == "TERMINATED":
			report.Terminated++
			err = requestRemoval(ctx, tx, now, &actor.EmployeeID, grant, *employee, "TERMINATION", "account of a terminated employee")
		case status == "SUSPENDED":
			err = suspendIfSuspended(ctx, tx, now, grant, *employee)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// verifyGrant marks a reported baseline grant as found in the tool.
func verifyGrant(ctx context.Context, tx pgx.Tx, now time.Time, actor auth.Principal, grant uuid.UUID) error {
	var employee *uuid.UUID
	if err := tx.QueryRow(ctx, "UPDATE grants SET tool_verified_at = $2 WHERE id = $1 RETURNING employee_id", grant, now).Scan(&employee); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "GRANT_VERIFIED_BY_TOOL", Entity: "grant",
		EntityID: &grant, SubjectID: employee})
}

// SaveReport writes the import report and its ACCESS_IMPORTED event, last in the import's
// transaction (the grants reference it, deferred).
func SaveReport(ctx context.Context, tx pgx.Tx, actor auth.Principal, report *ImportReport) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO imports (id, source, file_name, imported_by, imported_at, report) VALUES ($1, $2, $3, $4, $5, $6)`,
		report.ID, report.Source, report.FileName, actor.EmployeeID, report.ImportedAt, raw); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "ACCESS_IMPORTED", Entity: "import", EntityID: &report.ID,
		Details: map[string]any{"source": report.Source, "fileName": report.FileName, "imported": report.Imported, "orphans": report.Orphans,
			"skipped": report.Skipped, "rejected": len(report.Rejected), "created": report.Created}})
}

// Imports lists the import reports, newest first, for Super Admins and InfoSec.
func (s *Service) Imports(ctx context.Context, actor auth.Principal) ([]ImportReport, error) {
	if !actor.HasRole("SUPER_ADMIN") && !actor.HasRole("INFOSEC") {
		return nil, platform.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, "SELECT report FROM imports ORDER BY imported_at DESC, id DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ImportReport, error) {
		var r ImportReport
		err := row.Scan(&r)
		return r, err
	})
}

// Orphan is an imported account that matches no employee (D10), until InfoSec revokes or
// dismisses it.
type Orphan struct {
	ID            uuid.UUID `json:"id"`
	ToolID        uuid.UUID `json:"toolId"`
	Tool          string    `json:"tool"`
	AccessLevelID uuid.UUID `json:"accessLevelId"`
	Level         string    `json:"level"`
	Method        string    `json:"method"`
	Account       string    `json:"account"`
	Status        string    `json:"status"`
	Source        string    `json:"source"`
	FileName      *string   `json:"fileName"`
	ImportedAt    time.Time `json:"importedAt"`
	Version       int       `json:"version"`
}

// openOrphan is an orphan still waiting for InfoSec.
const openOrphan = "g.orphan_dismissed_at IS NULL AND g.status IN ('ACTIVE', 'SUSPENDED')"

// OrphanAccounts lists the first open orphans and how many there are (Exceptions). The caller
// checks who may read them.
func OrphanAccounts(ctx context.Context, pool *pgxpool.Pool) ([]Orphan, int, error) {
	list, err := orphans(ctx, pool, openOrphan)
	if err != nil {
		return nil, 0, err
	}
	total := len(list)
	if total == orphanLimit {
		err = pool.QueryRow(ctx, "SELECT count(*) FROM grants g WHERE g.employee_id IS NULL AND g.import_id IS NOT NULL AND "+openOrphan).Scan(&total)
	}
	return list, total, err
}

const orphanLimit = 500 // shortcut: first 500 by tool and account, with the total; each resolved one makes room

func orphans(ctx context.Context, pool *pgxpool.Pool, where string, args ...any) ([]Orphan, error) {
	rows, err := pool.Query(ctx, `SELECT g.id, t.id, t.name, l.id, l.name, g.method, g.account, g.status, i.source, i.file_name,
		       i.imported_at, g.version
		FROM grants g JOIN imports i ON i.id = g.import_id JOIN tools t ON t.id = g.tool_id JOIN access_levels l ON l.id = g.access_level_id
		WHERE g.employee_id IS NULL AND `+where+` ORDER BY t.name, g.account, l.name, g.id LIMIT `+strconv.Itoa(orphanLimit), args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Orphan])
}

// RevokeOrphan removes an orphan account through the normal removal (D10).
func (s *Service) RevokeOrphan(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, reason string) (Orphan, error) {
	return s.orphanAction(ctx, actor, id, version, reason, func(tx pgx.Tx, now time.Time) error {
		return Remove(ctx, tx, now, &actor.EmployeeID, id, "ORPHAN", reason)
	})
}

// DismissOrphan closes an orphan finding, leaving the account as it is, with a reason.
func (s *Service) DismissOrphan(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, reason string) (Orphan, error) {
	return s.orphanAction(ctx, actor, id, version, reason, func(tx pgx.Tx, now time.Time) error {
		if _, err := tx.Exec(ctx, `UPDATE grants SET orphan_dismissed_at = $2, orphan_dismissed_by = $3, orphan_dismiss_reason = $4,
			version = version + 1 WHERE id = $1`, id, now, actor.EmployeeID, reason); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{ActorID: &actor.EmployeeID, Action: "ORPHAN_DISMISSED", Entity: "grant", EntityID: &id,
			Reason: reason})
	})
}

// orphanAction runs do on an open orphan, for InfoSec (D10), at the version they saw.
func (s *Service) orphanAction(ctx context.Context, actor auth.Principal, id uuid.UUID, version int, reason string,
	do func(tx pgx.Tx, now time.Time) error) (Orphan, error) {
	if !actor.HasRole("INFOSEC") {
		return Orphan{}, platform.ErrForbidden
	}
	if strings.TrimSpace(reason) == "" {
		return Orphan{}, platform.ValidationError("reason", "reason_required")
	}
	if err := platform.CheckText(reason, "reason", 1000); err != nil {
		return Orphan{}, err
	}
	err := platform.InTx(ctx, s.pool, func(tx pgx.Tx, now time.Time) error {
		// Lock order: operations, then the grant (see Runner).
		if _, err := tx.Exec(ctx, "SELECT 1 FROM operations WHERE grant_id = $1 FOR UPDATE", id); err != nil {
			return err
		}
		if p, err := CurrentActor(ctx, tx, actor); err != nil {
			return err
		} else if !p.HasRole("INFOSEC") { // removed while the request waited
			return platform.ErrForbidden
		}
		var current int
		var isOpen bool
		err := tx.QueryRow(ctx, "SELECT g.version, "+openOrphan+" FROM grants g WHERE g.id = $1 AND g.employee_id IS NULL FOR UPDATE",
			id).Scan(&current, &isOpen)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return platform.ErrNotFound
		case err != nil:
			return err
		case current != version:
			return errVersionConflict
		case !isOpen:
			return errOrphanClosed
		}
		return do(tx, now)
	})
	if err != nil {
		return Orphan{}, err
	}
	list, err := orphans(ctx, s.pool, "g.id = $1", id)
	if err != nil || len(list) == 0 {
		return Orphan{}, err
	}
	return list[0], nil
}
