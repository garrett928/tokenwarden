package api

import (
	"net/http"
	"testing"
)

func TestUsage_EmptyLedger(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/usage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decode[UsageResponse](t, resp)
	if got.FiveHour.EntryCount != 0 || got.SevenDay.EntryCount != 0 {
		t.Errorf("usage on an empty ledger = %+v, want zero entries", got)
	}
	if !got.FiveHourCalibration.Insufficient {
		t.Errorf("FiveHourCalibration.Insufficient = false on an empty ledger, want true")
	}
	if !got.SevenDayCalibration.Insufficient {
		t.Errorf("SevenDayCalibration.Insufficient = false on an empty ledger, want true")
	}
}

func TestUsage_AfterDispatch(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	srv := newDispatchTestServer(t)

	created := decode[JobResponse](t, postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{
		Kind: "research", Prompt: "say pong",
	}))
	dispatchResp := postJSON(t, srv.URL+"/api/jobs/"+created.ID+"/dispatch", nil)
	if dispatchResp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch status = %d, want 202", dispatchResp.StatusCode)
	}
	pollUntilTerminal(t, srv, created.ID)

	resp, err := http.Get(srv.URL + "/api/usage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := decode[UsageResponse](t, resp)
	if got.FiveHour.EntryCount == 0 {
		t.Error("FiveHour.EntryCount = 0 after a dispatch, want the fixture's usage recorded")
	}
	if got.FiveHour.CostUSD != 0.0230845 {
		t.Errorf("FiveHour.CostUSD = %v, want the fixture's total_cost_usd 0.0230845", got.FiveHour.CostUSD)
	}
	if got.SevenDay.EntryCount != got.FiveHour.EntryCount {
		t.Errorf("SevenDay.EntryCount = %d, want to match FiveHour.EntryCount %d (same recent entry falls in both windows)", got.SevenDay.EntryCount, got.FiveHour.EntryCount)
	}
}
