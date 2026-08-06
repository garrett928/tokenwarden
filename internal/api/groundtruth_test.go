package api

import (
	"net/http"
	"testing"
	"time"
)

func TestRecordGroundTruth_Success(t *testing.T) {
	srv := newTestServer(t)
	now := time.Now()

	resp := postJSON(t, srv.URL+"/api/ground-truth", RecordGroundTruthRequest{
		FiveHour: RateLimitWindow{UsedPercentage: 89, ResetsAt: now.Add(time.Hour).Unix()},
		SevenDay: RateLimitWindow{UsedPercentage: 22, ResetsAt: now.Add(48 * time.Hour).Unix()},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	got := decode[GroundTruthResponse](t, resp)
	if got.FiveHour.UsedPercentage != 89 || got.SevenDay.UsedPercentage != 22 {
		t.Errorf("recorded reading = %+v, want 89/22", got)
	}
}

func TestRecordGroundTruth_MissingResetsAt(t *testing.T) {
	srv := newTestServer(t)
	resp := postJSON(t, srv.URL+"/api/ground-truth", RecordGroundTruthRequest{
		FiveHour: RateLimitWindow{UsedPercentage: 89},
		SevenDay: RateLimitWindow{UsedPercentage: 22, ResetsAt: time.Now().Unix()},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (five_hour.resets_at missing)", resp.StatusCode)
	}
}

func TestRecordGroundTruth_InvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Post(srv.URL+"/api/ground-truth", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an empty body", resp.StatusCode)
	}
}

func TestUsage_IncludesGroundTruthAfterRecording(t *testing.T) {
	srv := newTestServer(t)
	now := time.Now()

	postJSON(t, srv.URL+"/api/ground-truth", RecordGroundTruthRequest{
		FiveHour: RateLimitWindow{UsedPercentage: 89, ResetsAt: now.Add(time.Hour).Unix()},
		SevenDay: RateLimitWindow{UsedPercentage: 22, ResetsAt: now.Add(48 * time.Hour).Unix()},
	})

	resp, err := http.Get(srv.URL + "/api/usage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := decode[UsageResponse](t, resp)
	if got.GroundTruth == nil {
		t.Fatal("UsageResponse.GroundTruth is nil after recording a reading")
	}
	if got.GroundTruth.FiveHour.UsedPercentage != 89 {
		t.Errorf("GroundTruth.FiveHour.UsedPercentage = %d, want 89", got.GroundTruth.FiveHour.UsedPercentage)
	}
	if got.GroundTruth.AgeSeconds < 0 {
		t.Errorf("GroundTruth.AgeSeconds = %d, want >= 0", got.GroundTruth.AgeSeconds)
	}
}

func TestUsage_GroundTruthNilWhenNoneRecorded(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/usage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := decode[UsageResponse](t, resp)
	if got.GroundTruth != nil {
		t.Errorf("GroundTruth = %+v, want nil on a fresh store", got.GroundTruth)
	}
}
