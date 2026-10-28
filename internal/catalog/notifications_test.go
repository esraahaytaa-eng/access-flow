package catalog

import (
	"testing"

	"github.com/google/uuid"

	"github.com/esraahaytaa-eng/access-flow/internal/testutil"
)

// The notification API shows and changes only the caller's own notifications.
func TestNotificationAPIIsOwnOnly(t *testing.T) {
	env := newEnv(t)
	_, admin := env.user("SUPER_ADMIN")
	_, infosec := env.user("INFOSEC")
	_, other := env.user("INFOSEC")
	_, employee := env.user()
	env.newLevel(admin, env.newTool(admin, "Jira", "API"), "Viewer", false, false)

	mine := infosec.Do("GET", "/api/v1/notifications", nil)
	testutil.RequireStatus(t, mine, 200, "")
	list := mine.Body["notifications"].([]any)
	if len(list) != 1 {
		t.Fatalf("notifications = %d; want 1", len(list))
	}
	id := list[0].(map[string]any)["id"].(string)
	if n := len(employee.Do("GET", "/api/v1/notifications", nil).Body["notifications"].([]any)); n != 0 {
		t.Fatalf("employee sees %d notifications; want 0", n)
	}

	testutil.RequireStatus(t, other.Do("POST", "/api/v1/notifications/"+id+"/read", nil), 404, "not_found")
	testutil.RequireStatus(t, infosec.Do("POST", "/api/v1/notifications/not-a-uuid/read", nil), 404, "not_found")
	testutil.RequireStatus(t, infosec.Do("POST", "/api/v1/notifications/"+uuid.NewString()+"/read", nil), 404, "not_found")
	for range 2 {
		testutil.RequireStatus(t, infosec.Do("POST", "/api/v1/notifications/"+id+"/read", nil), 204, "")
	}
	read := infosec.Do("GET", "/api/v1/notifications", nil).Body["notifications"].([]any)[0].(map[string]any)
	if read["readAt"] == nil {
		t.Fatal("readAt not set")
	}
	if unread := other.Do("GET", "/api/v1/notifications", nil).Body["notifications"].([]any)[0].(map[string]any); unread["readAt"] != nil {
		t.Fatal("marking mine read changed someone else's")
	}
}
