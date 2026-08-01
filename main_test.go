package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fjacquet/obs_exporter/internal/ecs"
)

func TestLivezReturnsOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	rec := httptest.NewRecorder()

	staticOKHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("body = %q, want %q", got, "ok")
	}
}

func TestReadyzReturnsOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	staticOKHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("body = %q, want %q", got, "ok")
	}
}

func TestHealthReturns200WhenClusterUnhealthy(t *testing.T) {
	store := ecs.NewSnapshotStore()
	store.Store(&ecs.Snapshot{
		BuiltAt: time.Now(),
		Clusters: []*ecs.ClusterSnapshot{
			{Cluster: "ecs-dr-02", OK: false, Err: "all 6 collectors failed: login GET: status 401"},
		},
	})

	rec := httptest.NewRecorder()
	healthHandler(rec, store)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Clusters []struct {
			Cluster string `json:"cluster"`
			OK      bool   `json:"ok"`
			Err     string `json:"err"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Clusters) != 1 || body.Clusters[0].OK {
		t.Fatalf("clusters = %+v, want one cluster with ok=false", body.Clusters)
	}
	if body.Clusters[0].Err == "" {
		t.Fatalf("err field empty, want the collector failure message")
	}
}

func TestHealthReturns200WhenNoClusters(t *testing.T) {
	store := ecs.NewSnapshotStore()

	rec := httptest.NewRecorder()
	healthHandler(rec, store)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
