package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFixedCommandAdapterAndPrivateErrors(t *testing.T) {
	for _, tc := range []struct {
		direction string
		operation int
	}{{"stop", 0}, {"left", 1}, {"right", 2}, {"up", 3}, {"down", 4}} {
		t.Run(tc.direction, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if r.Method != "POST" || r.URL.Path != "/api/xiaomi/command" || q.Get("src") != "camera" || q.Get("cmd") != "0x112" || q.Get("data") != fmt.Sprintf("{\"operation\":%d}", tc.operation) {
					t.Errorf("wrong command: %s", r.URL)
				}
				http.Error(w, "SECRET camera token", 500)
			}))
			defer server.Close()
			p := &proxy{name: "camera", go2rtcURL: server.URL, httpClient: server.Client()}
			err := p.sendPTZ(context.Background(), tc.direction, 0)
			if err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("error leaked or missing: %v", err)
			}
		})
	}
}
func TestManualCapabilitiesAndUnsupportedMoves(t *testing.T) {
	p := &proxy{name: "camera"}
	for _, text := range []string{p.getProfiles(), p.getConfigurationOptions(), p.getNodes()} {
		if strings.Contains(text, "TranslationSpaceFov") || strings.Contains(text, "AbsolutePanTilt") {
			t.Fatal("unsupported capability")
		}
	}
	for _, action := range []string{"RelativeMove", "GotoPreset", "AbsoluteMove"} {
		rr := httptest.NewRecorder()
		p.ServeHTTP(rr, httptest.NewRequest("POST", "/onvif/ptz_service", strings.NewReader(envelope("<tptz:"+action+"/>"))))
		if !strings.Contains(rr.Body.String(), "ActionNotSupported") {
			t.Fatal(rr.Body.String())
		}
	}
	presets, err := p.getPresets()
	if err != nil || presets != "<tptz:GetPresetsResponse/>" {
		t.Fatal("presets")
	}
}
func TestLostReleaseExpiresWithoutFlooding(t *testing.T) {
	var mu sync.Mutex
	var calls []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := 1
		if strings.Contains(r.URL.Query().Get("data"), ":0") {
			op = 0
		}
		mu.Lock()
		calls = append(calls, op)
		mu.Unlock()
		fmt.Fprint(w, "{}")
	}))
	defer server.Close()
	p := &proxy{name: "camera", go2rtcURL: server.URL, httpClient: server.Client()}
	p.startMove("left")
	muDone := p.moveDone
	select {
	case <-muDone:
	case <-time.After(3 * time.Second):
		t.Fatal("movement did not expire")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) < 2 || len(calls) > 6 || calls[len(calls)-1] != 0 || p.moveStatus() != "IDLE" {
		t.Fatalf("unsafe sequence: %v", calls)
	}
}
