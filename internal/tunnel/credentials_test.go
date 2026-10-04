package tunnel

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"andey-proxy/internal/config"
)

func TestTokenIdentityChecksRejectMismatchesWithoutSavingOrLaunching(t *testing.T) {
	for _, token := range []string{
		encodedTestToken(testAccountID, "99999999-2222-3333-4444-555555555555", "secret"),
		encodedTestToken("dddddddddddddddddddddddddddddddd", testTunnelID, "secret"),
		"not-base64",
		encodedTestToken(testAccountID, testTunnelID, ""),
	} {
		t.Run(fmt.Sprint(len(token), token[:4]), func(t *testing.T) {
			m, f, h := newTestManager(t)
			body := fmt.Sprintf(`{"name":"test","source":"managed","accountRef":"account","tunnelId":%q,"token":%q}`, testTunnelID, token)
			resp := request(t, h, "PUT", "/api/tunnels/instances/instance", body)
			if resp.Code != 400 || strings.Contains(resp.Body.String(), token) {
				t.Fatal(resp.Body.String())
			}
			inst, _ := m.instance("instance")
			if inst.Token != testTunnelToken {
				t.Fatal("failed validation changed credentials")
			}
			inst.Token = token
			if err := m.saveInstance(inst); err != nil {
				t.Fatal(err)
			} // Simulate an older config/backup.
			resp = request(t, h, "POST", "/api/tunnels/instances/instance/start", "")
			if resp.Code != 400 {
				t.Fatal("invalid saved identity could start")
			}
			m.runtime = func() Runtime { t.Fatal("invalid identity reached process setup"); return Runtime{} }
			if err := m.runProcess(context.Background(), &worker{instance: inst}); err == nil {
				t.Fatal("invalid identity could launch")
			}
			if _, err := m.cloud(inst); err == nil {
				t.Fatal("invalid identity could manage cloud resources")
			}
			if f.reads != 0 || f.writes != 0 {
				t.Fatal("invalid identity contacted Cloudflare")
			}
		})
	}
}

func TestTokenOnlyImportExtractsTunnelIDAndCanBindMatchingAccount(t *testing.T) {
	m, _, h := newTestManager(t)
	id := "99999999-2222-3333-4444-555555555555"
	token := encodedTestToken(testAccountID, id, "secret")
	body := fmt.Sprintf(`{"name":"imported","source":"imported","token":%q}`, token)
	resp := request(t, h, "POST", "/api/tunnels/instances", body)
	if resp.Code != 200 || strings.Contains(resp.Body.String(), token) {
		t.Fatal(resp.Body.String())
	}
	var imported config.TunnelInstance
	for _, inst := range m.instances() {
		if inst.TunnelID == id {
			imported = inst
		}
	}
	if imported.ID == "" || imported.AccountRef != "" || imported.Token != token {
		t.Fatal("token-only identity not extracted")
	}
	body = fmt.Sprintf(`{"name":"imported","source":"imported","accountRef":"account","tunnelId":%q,"token":""}`, id)
	resp = request(t, h, "PUT", "/api/tunnels/instances/"+imported.ID, body)
	if resp.Code != 200 {
		t.Fatal(resp.Body.String())
	}
	imported, _ = m.instance(imported.ID)
	if imported.Token != token || imported.AccountRef != "account" {
		t.Fatal("matching binding changed credentials")
	}
}
