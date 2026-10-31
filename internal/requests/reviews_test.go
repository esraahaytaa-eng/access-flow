package requests

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// reviewWorld has dev's Jira Viewer and GitHub Developer and the owner's own Jira Viewer,
// all ACTIVE, and a running Mock Tool Service.
func reviewWorld(t *testing.T) (*world, *tools) {
	w := newWorld(t)
	tl := w.startTools()
	w.approved(w.jiraViewer)
	gh := w.mustSubmit(w.dev.c, body(nil, item(w.github)))
	w.approve(w.owner.c, open(t, w.approve(w.mgr.c, open(t, items(gh)[0]))))
	own := w.mustSubmit(w.owner.c, body(nil, item(w.jiraViewer)))
	w.approve(w.is1.c, open(t, items(own)[0])) // the owner has no manager: InfoSec decides
	tl.drain(t)
	return w, tl
}

func (w *world) startCampaign(p person) testutil.Response {
	return p.c.Do(http.MethodPost, "/api/v1/reviews/campaigns", map[string]any{})
}

// reviewItems is what p sees on the review page.
func (w *world) reviewItems(p person) []map[string]any {
	w.t.Helper()
	r := w.get(p.c, "/api/v1/reviews?view=all")
	testutil.RequireStatus(w.t, r, http.StatusOK, "")
	var out []map[string]any
	for _, it := range r.Body["items"].([]any) {
		out = append(out, it.(map[string]any))
	}
	return out
}

// reviewItem is p's item for employee's level.
func (w *world) reviewItem(p person, employee, level uuid.UUID) map[string]any {
	w.t.Helper()
	for _, it := range w.reviewItems(p) {
		if it["employee"].(map[string]any)["id"] == employee.String() && it["accessLevelId"] == level.String() {
			return it
		}
	}
	w.t.Fatalf("no review item for %v / %v", employee, level)
	return nil
}

func decideReview(p person, it map[string]any, decision, reason string) testutil.Response {
	return p.c.Do(http.MethodPost, "/api/v1/review-items/"+it["id"].(string)+"/decide",
		map[string]any{"decision": decision, "reason": reason, "version": it["version"]})
}

// age moves the active campaign's start days into the past.
func (w *world) age(days int) {
	w.exec("UPDATE review_campaigns SET started_at = started_at - make_interval(days => $1) WHERE closed_at IS NULL", days)
}

func TestCampaignSnapshotAndRouting(t *testing.T) {
	t.Parallel()
	w, _ := reviewWorld(t)
	w.mustSubmit(w.dev.c, body(nil, item(w.awsAdmin))) // pending: not access yet
	testutil.RequireStatus(t, w.startCampaign(w.owner), http.StatusForbidden, "forbidden")
	r := w.startCampaign(w.admin)
	testutil.RequireStatus(t, r, http.StatusCreated, "")
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusConflict, "campaign_active")

	all := w.reviewItems(w.is1)
	if len(all) != 3 {
		t.Fatalf("items = %d, want 3", len(all))
	}
	mine := w.reviewItems(w.owner)
	if len(mine) != 2 {
		t.Fatalf("the owner sees %d items, want dev's two", len(mine))
	}
	own := w.reviewItem(w.is1, w.owner.ID, w.jiraViewer)
	if own["reviewerId"] != nil || own["routingNote"] != "own access" {
		t.Fatalf("own access routed to %v (%v)", own["reviewerId"], own["routingNote"])
	}
	if dev := w.reviewItem(w.owner, w.dev.ID, w.github); dev["reviewerId"] != w.owner.ID.String() {
		t.Fatalf("dev's item reviewer = %v", dev["reviewerId"])
	}
	if w.audits("REVIEW_CAMPAIGN_STARTED") != 1 || w.count("SELECT count(*) FROM notifications WHERE kind = 'REVIEW_STARTED' AND recipient_id = $1", w.owner.ID) != 1 {
		t.Fatal("campaign start not audited or the owner not notified")
	}
}

func TestReviewDecisions(t *testing.T) {
	t.Parallel()
	w, tl := reviewWorld(t)
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	viewer := w.reviewItem(w.owner, w.dev.ID, w.jiraViewer)

	testutil.RequireStatus(t, decideReview(w.is1, viewer, "KEEP", ""), http.StatusForbidden, "not_reviewer")
	testutil.RequireStatus(t, decideReview(w.admin, viewer, "KEEP", ""), http.StatusForbidden, "super_admin_keep_forbidden")
	testutil.RequireStatus(t, decideReview(w.dev, viewer, "KEEP", ""), http.StatusForbidden, "separation_of_duties")
	testutil.RequireStatus(t, decideReview(w.owner, viewer, "MAYBE", ""), http.StatusBadRequest, "invalid_decision")
	kept := decideReview(w.owner, viewer, "KEEP", "")
	testutil.RequireStatus(t, kept, http.StatusOK, "")
	if kept.Body["decision"] != "KEEP" || kept.Body["late"] != false {
		t.Fatalf("kept item = %v", kept.Body)
	}
	testutil.RequireStatus(t, decideReview(w.owner, viewer, "REVOKE", "changed my mind"), http.StatusConflict, "version_conflict")
	testutil.RequireStatus(t, decideReview(w.owner, kept.Body, "REVOKE", "changed my mind"), http.StatusConflict, "review_item_closed")

	gh := w.reviewItem(w.owner, w.dev.ID, w.github)
	testutil.RequireStatus(t, decideReview(w.owner, gh, "REVOKE", " "), http.StatusBadRequest, "reason_required")
	testutil.RequireStatus(t, decideReview(w.admin, gh, "REVOKE", "not needed after the project"), http.StatusOK, "")
	g := w.myGrant(w.dev.c, w.github)
	if g["status"] != "REVOCATION_REQUESTED" || w.count("SELECT count(*) FROM operations WHERE grant_id = $1 AND type = 'REVOKE' AND reason = 'REVIEW'", g["id"]) != 1 {
		t.Fatalf("revoke decision: grant %v", g["status"])
	}
	tl.drain(t)
	if w.audits("REVIEW_KEPT") != 1 || w.audits("REVIEW_REVOKED") != 1 {
		t.Fatal("decisions not audited")
	}
}

func TestBulkKeepSkipsItemsDecidedIndividually(t *testing.T) {
	t.Parallel()
	w, tl := reviewWorld(t)
	// Jira Admin requires InfoSec approval, so its item is decided individually.
	req := w.mustSubmit(w.mgr.c, body(nil, item(w.jiraAdmin)))
	it := w.approve(w.skip.c, open(t, items(req)[0]))
	w.approve(w.is1.c, open(t, w.approve(w.owner.c, open(t, it))))
	tl.drain(t)
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	var batch []map[string]any
	for _, it := range w.reviewItems(w.owner) {
		batch = append(batch, map[string]any{"id": it["id"], "version": it["version"]})
	}
	r := w.owner.c.Do(http.MethodPost, "/api/v1/review-items/bulk-keep", map[string]any{"items": batch})
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["kept"] != float64(2) || r.Body["skipped"] != float64(len(batch)-2) {
		t.Fatalf("bulk keep = %v of %d", r.Body, len(batch))
	}
	if it := w.reviewItem(w.owner, w.dev.ID, w.jiraViewer); it["decision"] != "KEEP" {
		t.Fatalf("viewer = %v", it["decision"])
	}
}

func TestDayStepsRunOnceAndUseTheSnapshot(t *testing.T) {
	t.Parallel()
	w, tl := reviewWorld(t)
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	// GitHub Developer gains an InfoSec step after the start: day 21 still uses the snapshot.
	w.exec("UPDATE access_levels SET requires_infosec = true WHERE id = $1", w.github)
	w.exec("UPDATE access_levels SET requires_infosec = true WHERE id = $1", w.jiraViewer)
	w.exec("UPDATE review_items SET requires_infosec = true WHERE grant_id IN (SELECT id FROM grants WHERE access_level_id = $1 AND employee_id = $2)",
		w.jiraViewer, w.dev.ID) // dev's Viewer counts as an InfoSec level for this campaign

	w.age(6)
	w.sweep()
	if n := w.count("SELECT count(*) FROM notifications WHERE kind = 'REVIEW_REMINDER'"); n != 0 {
		t.Fatalf("reminders before day 7 = %d", n)
	}
	w.age(1)
	w.sweep()
	w.sweep()
	// One per open item: two to the owner, and the pool item's to every InfoSec member.
	if n := w.count("SELECT count(*) FROM notifications WHERE kind = 'REVIEW_REMINDER'"); n != 4 {
		t.Fatalf("day-7 reminders = %d, want 4", n)
	}
	viewer := w.reviewItem(w.owner, w.dev.ID, w.jiraViewer)
	testutil.RequireStatus(t, decideReview(w.is1, viewer, "KEEP", ""), http.StatusForbidden, "not_reviewer")

	w.age(7)
	w.sweep()
	w.sweep()
	if w.audits("REVIEW_ESCALATED") != 1 {
		t.Fatalf("escalations = %d", w.audits("REVIEW_ESCALATED"))
	}
	if r := w.get(w.is1.c, "/api/v1/reviews"); r.Body["campaign"].(map[string]any)["escalatedAt"] == nil {
		t.Fatal("campaign not escalated")
	}

	w.age(7)
	w.sweep()
	w.sweep()
	tl.drain(t)
	// A missed deadline never removes an active employee's access, InfoSec level or not
	// (D8 note 2026-10-30): every unanswered item becomes an open finding.
	for _, level := range []uuid.UUID{w.jiraViewer, w.github} {
		if g := w.myGrant(w.dev.c, level); g["status"] != "ACTIVE" {
			t.Fatalf("item on day 21: %v (%v)", g["status"], g["closedReason"])
		}
	}
	if n := w.count("SELECT count(*) FROM review_items WHERE finding_at IS NOT NULL"); n != 3 {
		t.Fatalf("findings = %d, want 3", n)
	}
	if w.audits("REVIEW_AUTO_REVOKED") != 0 || w.count("SELECT count(*) FROM review_campaigns WHERE closed_at IS NOT NULL") != 1 {
		t.Fatal("day 21 revoked access or did not close the campaign")
	}
	ex := w.get(w.is1.c, "/api/v1/exceptions")
	if n := len(ex.Body["reviewFindings"].([]any)); n != 3 {
		t.Fatalf("open findings in Exceptions = %d", n)
	}
}

func TestOpenFindingIsCarriedNotReAdded(t *testing.T) {
	t.Parallel()
	w, _ := reviewWorld(t)
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	w.age(21)
	w.sweep()
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	if n := w.count("SELECT count(*) FROM review_items i JOIN review_campaigns c ON c.id = i.campaign_id WHERE c.closed_at IS NULL"); n != 0 {
		t.Fatalf("grants with open findings added again: %d", n)
	}
	finding := w.reviewItem(w.owner, w.dev.ID, w.github)
	if finding["findingAt"] == nil {
		t.Fatalf("finding not listed for its reviewer: %v", finding)
	}
	r := decideReview(w.owner, finding, "KEEP", "")
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if r.Body["late"] != true {
		t.Fatalf("late decision = %v", r.Body["late"])
	}
	ex := w.get(w.is1.c, "/api/v1/exceptions")
	if n := len(ex.Body["reviewFindings"].([]any)); n != 2 {
		t.Fatalf("open findings after one late decision = %d, want 2", n)
	}
}

func TestItemOfRemovedGrantClosesAsAlreadyRemoved(t *testing.T) {
	t.Parallel()
	w, tl := reviewWorld(t)
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, w.jiraViewer))
	w.sweep()
	if it := w.reviewItem(w.owner, w.dev.ID, w.jiraViewer); it["closedReason"] != nil {
		t.Fatalf("closed while still being removed: %v", it["closedReason"])
	}
	tl.drain(t)
	w.sweep()
	w.sweep()
	if it := w.reviewItem(w.owner, w.dev.ID, w.jiraViewer); it["closedReason"] != "ALREADY_REMOVED" {
		t.Fatalf("closed reason = %v", it["closedReason"])
	}
	if w.audits("REVIEW_ITEM_CLOSED") != 1 {
		t.Fatalf("closures audited = %d", w.audits("REVIEW_ITEM_CLOSED"))
	}
}

func TestQuarterlyCampaignStartsOncePerQuarter(t *testing.T) {
	t.Parallel()
	w, _ := reviewWorld(t)
	w.sweep()
	if n := w.count("SELECT count(*) FROM review_campaigns"); n != 0 {
		t.Fatalf("a campaign started in the quarter AccessFlow was installed: %d", n)
	}
	w.exec("UPDATE employees SET created_at = created_at - interval '1 year' WHERE id = $1", w.admin.ID)
	w.sweep()
	w.sweep()
	if n := w.count("SELECT count(*) FROM review_campaigns WHERE kind = 'QUARTERLY' AND started_by IS NULL"); n != 1 {
		t.Fatalf("quarterly campaigns = %d", n)
	}
	w.age(21)
	w.sweep()
	w.sweep()
	if n := w.count("SELECT count(*) FROM review_campaigns"); n != 1 {
		t.Fatalf("a second campaign started in the same quarter: %d", n)
	}
}

func TestAddedStepFlagsGrantsForIndividualReview(t *testing.T) {
	t.Parallel()
	w, _ := reviewWorld(t)
	g := w.myGrant(w.dev.c, w.jiraViewer)
	r := w.admin.c.Do(http.MethodPut, "/api/v1/tools/"+w.jira.String()+"/levels/"+w.jiraViewer.String(),
		map[string]any{"name": "Viewer", "requiresToolOwner": true, "requiresInfosec": false, "version": 1})
	testutil.RequireStatus(t, r, http.StatusOK, "")
	if n := w.count("SELECT count(*) FROM grants WHERE access_level_id = $1 AND earlier_policy_at IS NOT NULL", w.jiraViewer); n != 2 {
		t.Fatalf("flagged grants = %d, want dev's and the owner's", n)
	}
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	it := w.reviewItem(w.owner, w.dev.ID, w.jiraViewer)
	if flags := it["flags"].([]any); len(flags) != 1 || flags[0] != "EARLIER_POLICY" {
		t.Fatalf("flags = %v", it["flags"])
	}
	testutil.RequireStatus(t, decideReview(w.owner, it, "KEEP", ""), http.StatusOK, "")
	if w.count("SELECT count(*) FROM grants WHERE id = $1 AND earlier_policy_at IS NULL", g["id"]) != 1 {
		t.Fatal("a Keep decision did not clear the flag")
	}
}

func TestEarlierPolicyFlagAddedDuringACampaignSurvivesItsDecision(t *testing.T) {
	t.Parallel()
	w, _ := reviewWorld(t)
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	r := w.admin.c.Do(http.MethodPut, "/api/v1/tools/"+w.jira.String()+"/levels/"+w.jiraViewer.String(),
		map[string]any{"name": "Viewer", "requiresToolOwner": true, "requiresInfosec": false, "version": 1})
	testutil.RequireStatus(t, r, http.StatusOK, "")
	g := w.myGrant(w.dev.c, w.jiraViewer)
	testutil.RequireStatus(t, decideReview(w.owner, w.reviewItem(w.owner, w.dev.ID, w.jiraViewer), "KEEP", ""), http.StatusOK, "")
	if w.count("SELECT count(*) FROM grants WHERE id = $1 AND earlier_policy_at IS NOT NULL", g["id"]) != 1 {
		t.Fatal("a decision on an item snapshotted before the step was added cleared the flag")
	}
}

func TestGrantBeingRemovedIsNeitherDecidedNorAFinding(t *testing.T) {
	t.Parallel()
	w, _ := reviewWorld(t)
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	w.exec("UPDATE review_items SET requires_infosec = true")
	it := w.reviewItem(w.owner, w.dev.ID, w.jiraViewer)
	w.mustRevoke(w.dev.c, w.myGrant(w.dev.c, w.jiraViewer)) // REVOCATION_REQUESTED, the runner not run
	testutil.RequireStatus(t, decideReview(w.owner, it, "KEEP", ""), http.StatusConflict, "review_item_closed")
	w.age(21)
	w.sweep()
	if n := w.count("SELECT count(*) FROM review_items WHERE grant_id = $1 AND (closed_reason IS NOT NULL OR finding_at IS NOT NULL)", it["grantId"]); n != 0 {
		t.Fatalf("day 21 acted on a grant already being removed: %d", n)
	}
	if w.audits("REVIEW_AUTO_REVOKED") != 0 || w.audits("REVIEW_FINDING_OPENED") != 2 || w.audits("REVIEW_REMINDERS_SENT") != 1 {
		t.Fatalf("day steps audited: auto %d, findings %d, reminders %d", w.audits("REVIEW_AUTO_REVOKED"), w.audits("REVIEW_FINDING_OPENED"), w.audits("REVIEW_REMINDERS_SENT"))
	}
}

// A terminated employee's access is never kept: no new Keep, the earlier Keep stays as
// history only, and termination's removal runs regardless of it.
func TestTerminatedEmployeeAccessCannotBeKept(t *testing.T) {
	t.Parallel()
	w, tl := reviewWorld(t)
	testutil.RequireStatus(t, w.startCampaign(w.admin), http.StatusCreated, "")
	viewer := w.reviewItem(w.owner, w.dev.ID, w.jiraViewer)
	testutil.RequireStatus(t, decideReview(w.owner, viewer, "KEEP", ""), http.StatusOK, "")
	w.reviewItem(w.owner, w.dev.ID, w.github)

	hr := w.hr()
	w.mustLifecycle(hr, w.dev.ID, "suspend")
	gh := w.reviewItem(w.owner, w.dev.ID, w.github) // suspension does not stop reviews (D7)
	if gh["employee"].(map[string]any)["status"] != "SUSPENDED" {
		t.Fatalf("employee status on the item = %v", gh["employee"])
	}
	w.mustLifecycle(hr, w.dev.ID, "terminate")
	testutil.RequireStatus(t, decideReview(w.owner, gh, "KEEP", ""), http.StatusConflict, "terminated_access_not_kept")
	bulk := w.owner.c.Do(http.MethodPost, "/api/v1/review-items/bulk-keep", map[string]any{"items": []any{map[string]any{"id": gh["id"], "version": gh["version"]}}})
	testutil.RequireStatus(t, bulk, http.StatusOK, "")
	if bulk.Body["kept"] != 0.0 || bulk.Body["skipped"] != 1.0 {
		t.Fatalf("bulk keep of a leaver's item = %v", bulk.Body)
	}

	kept := w.reviewItem(w.owner, w.dev.ID, w.jiraViewer)
	if kept["decision"] != "KEEP" || kept["grantStatus"] != "REVOCATION_REQUESTED" {
		t.Fatalf("kept item after termination: %v, grant %v", kept["decision"], kept["grantStatus"])
	}
	tl.drain(t)
	kept = w.reviewItem(w.owner, w.dev.ID, w.jiraViewer)
	if kept["decision"] != "KEEP" || kept["grantStatus"] != "REVOKED" || w.audits("REVIEW_KEPT") != 1 {
		t.Fatalf("history after removal: %v, grant %v, REVIEW_KEPT %d", kept["decision"], kept["grantStatus"], w.audits("REVIEW_KEPT"))
	}
}
