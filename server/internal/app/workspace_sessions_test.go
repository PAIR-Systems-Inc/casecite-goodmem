package app

import (
	"context"
	"strings"
	"testing"
)

func TestWorkspaceSnapshotsAndSharedChatSessions(t *testing.T) {
	f := newFixture(t)
	owner := f.admin()
	member := f.account(owner, "history-member@example.test")
	outsider := f.account(owner, "history-outsider@example.test")
	ownerID := str(f.call("GET", "/auth/me", owner, nil, 200), "id")
	memberID := str(f.call("GET", "/auth/me", member, nil, 200), "id")
	outsiderID := str(f.call("GET", "/auth/me", outsider, nil, 200), "id")
	f.call("POST", "/workspace-sessions", owner, M{"surface": "", "title": "Test"}, 400)
	f.call("POST", "/workspace-sessions", owner, M{"surface": "contracts", "title": "Test", "payload": []any{}}, 400)
	f.call("POST", "/workspace-sessions", owner, M{"surface": "contracts", "title": "Test", "payload": M{"text": strings.Repeat("x", 2000000)}}, 413)
	payload := M{"draft": "Payment is due within thirty days.", "selectedDocuments": []string{"doc-1"}, "redlines": []M{{"ref": "r1", "decision": "accept"}}}
	snapshot := f.call("POST", "/workspace-sessions", owner, M{"surface": "contracts", "title": "Services review", "payload": payload}, 201)
	sid := str(snapshot, "id")
	if _, exists := snapshot["payload"]; exists {
		t.Fatal("summary exposed snapshot")
	}
	f.call("GET", "/workspace-sessions/"+sid, outsider, nil, 404)
	f.call("PUT", "/workspace-sessions/"+sid, member, M{"title": "Changed"}, 404)
	f.call("PUT", "/workspace-sessions/"+sid, owner, M{"title": "Renamed review"}, 200)
	saved := f.call("GET", "/workspace-sessions/"+sid, owner, nil, 200)
	if str(saved, "title") != "Renamed review" || string(jsonBytes(saved["payload"])) != string(jsonBytes(payload)) {
		t.Fatal(saved)
	}
	f.call("GET", "/workspace-sessions?limit=0", owner, nil, 400)
	list := maps(f.call("GET", "/workspace-sessions?limit=1", owner, nil, 200)["sessions"])
	if len(list) != 1 || str(list[0], "id") != sid || list[0]["payload"] != nil {
		t.Fatal(list)
	}
	matter := f.call("POST", "/matters", owner, M{"name": "Shared history"}, 200)
	mid := str(matter, "id")
	f.call("POST", "/matters/"+mid+"/members", owner, M{"email": "history-member@example.test"}, 200)
	if _, err := f.s.db.Exec(`UPDATE cc_objects SET matter_id=$1 WHERE kind='workspace_session' AND id=$2`, mid, sid); err != nil {
		t.Fatal(err)
	}
	f.call("GET", "/workspace-sessions/"+sid, member, nil, 200)
	f.call("DELETE", "/workspace-sessions/"+sid, member, nil, 404)
	f.call("POST", "/chat/sessions", outsider, M{"title": "Unauthorized", "matter_id": mid}, 404)
	chat := f.call("POST", "/chat/sessions", member, M{"title": "  Payment discussion  ", "matter_id": mid}, 200)
	cid := str(chat, "id")
	if str(chat, "title") != "Payment discussion" || number(chat, "message_count", -1) != 0 {
		t.Fatal(chat)
	}
	for i, role := range []string{"user", "assistant", "user"} {
		uid := memberID
		if i == 2 {
			uid = ownerID
		}
		if _, err := f.s.appendChatMessage(context.Background(), uid, cid, role, "Message "+role, []M{{"quote": "Thirty days"}}, nil, M{"sources": 1}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.s.appendChatMessage(context.Background(), outsiderID, cid, "user", "Injected", nil, nil, nil); err == nil {
		t.Fatal("foreign user appended to shared thread")
	}
	thread := f.call("GET", "/chat/sessions/"+cid, owner, nil, 200)
	messages := maps(thread["messages"])
	if len(messages) != 3 || str(messages[1], "role") != "assistant" || len(maps(messages[1]["citations"])) != 1 {
		t.Fatal(thread)
	}
	chats := maps(f.call("GET", "/matters/"+mid+"/chats", owner, nil, 200)["sessions"])
	if len(chats) != 1 || number(chats[0], "message_count", 0) != 3 || chats[0]["messages"] != nil {
		t.Fatal(chats)
	}
	f.call("GET", "/chat/sessions/"+cid, outsider, nil, 404)
	f.call("DELETE", "/chat/sessions/"+cid, outsider, nil, 404)
	f.call("DELETE", "/matters/"+mid+"/members/"+memberID, owner, nil, 200)
	f.call("GET", "/workspace-sessions/"+sid, member, nil, 404)
	// The matter owner may delete a colleague's shared conversation; messages cascade.
	f.call("DELETE", "/chat/sessions/"+cid, owner, nil, 200)
	var count int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_chat_messages WHERE session_id=$1`, cid).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	f.call("DELETE", "/workspace-sessions/"+sid, owner, nil, 200)
}
