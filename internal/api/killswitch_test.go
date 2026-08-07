package api

import (
	"net/http"
	"testing"
)

func TestKillSwitch_StatusDefaultsToNotHalted(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/kill-switch")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := decode[KillSwitchResponse](t, resp)
	if got.Halted {
		t.Error("Halted = true on a fresh server, want false")
	}
}

func TestKillSwitch_HaltThenStatusReflectsIt(t *testing.T) {
	srv := newTestServer(t)

	resp := postJSON(t, srv.URL+"/api/kill-switch/halt", struct{}{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("halt status = %d, want 200", resp.StatusCode)
	}
	if got := decode[KillSwitchResponse](t, resp); !got.Halted {
		t.Error("halt response Halted = false, want true")
	}

	resp2, err := http.Get(srv.URL + "/api/kill-switch")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if got := decode[KillSwitchResponse](t, resp2); !got.Halted {
		t.Error("status after halt: Halted = false, want true")
	}
}

func TestKillSwitch_ResumeClearsIt(t *testing.T) {
	srv := newTestServer(t)
	postJSON(t, srv.URL+"/api/kill-switch/halt", struct{}{})

	resp := postJSON(t, srv.URL+"/api/kill-switch/resume", struct{}{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resume status = %d, want 200", resp.StatusCode)
	}
	if got := decode[KillSwitchResponse](t, resp); got.Halted {
		t.Error("resume response Halted = true, want false")
	}
}
