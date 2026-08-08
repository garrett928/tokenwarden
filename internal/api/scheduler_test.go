package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func putJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestGetSchedulerConfig_Default(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.Get(srv.URL + "/api/scheduler/config")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decode[SchedulerConfigResponse](t, resp)
	if got.Enabled {
		t.Errorf("Enabled = true by default, want false")
	}
}

func TestUpdateSchedulerConfig_RoundTrips(t *testing.T) {
	srv := newTestServer(t)

	budget := 25.0
	req := UpdateSchedulerConfigRequest{
		Enabled:        true,
		Aggressiveness: 70,
		ReservedBlocks: []TimeBlockDTO{{Days: []int{1, 2}, StartMin: 540, EndMin: 1020}},
		MaxBudgetUSD:   &budget,
	}

	resp := putJSON(t, srv.URL+"/api/scheduler/config", req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", resp.StatusCode)
	}
	updated := decode[SchedulerConfigResponse](t, resp)
	if !updated.Enabled || updated.Aggressiveness != 70 {
		t.Errorf("PUT response = %+v, want Enabled=true Aggressiveness=70", updated)
	}
	if len(updated.ReservedBlocks) != 1 || updated.ReservedBlocks[0].StartMin != 540 {
		t.Errorf("ReservedBlocks = %+v", updated.ReservedBlocks)
	}
	if updated.MaxBudgetUSD == nil || *updated.MaxBudgetUSD != budget {
		t.Errorf("MaxBudgetUSD = %v, want %v", updated.MaxBudgetUSD, budget)
	}

	getResp, err := http.Get(srv.URL + "/api/scheduler/config")
	if err != nil {
		t.Fatal(err)
	}
	got := decode[SchedulerConfigResponse](t, getResp)
	if !got.Enabled || got.Aggressiveness != 70 {
		t.Errorf("GET after PUT = %+v, want Enabled=true Aggressiveness=70", got)
	}
}

func TestUpdateSchedulerConfig_RejectsOutOfRangeAggressiveness(t *testing.T) {
	srv := newTestServer(t)

	resp := putJSON(t, srv.URL+"/api/scheduler/config", UpdateSchedulerConfigRequest{Aggressiveness: 150})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
