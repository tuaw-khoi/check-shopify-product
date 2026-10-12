package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func localRequest(method, target string, body []byte) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Host = "127.0.0.1:8787"
	request.RemoteAddr = "127.0.0.1:55123"
	return request
}

func TestDashboardAndLocalOnlyGuard(t *testing.T) {
	app, err := NewAppServer(URLGuard{AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}

	good := httptest.NewRecorder()
	app.Handler().ServeHTTP(good, localRequest(http.MethodGet, "http://127.0.0.1:8787/", nil))
	if good.Code != http.StatusOK || !strings.Contains(good.Body.String(), "Shopify Price Checker") {
		t.Fatalf("dashboard response: %d %s", good.Code, good.Body.String())
	}

	badRequest := httptest.NewRequest(http.MethodGet, "http://evil.example/", nil)
	badRequest.Host = "evil.example"
	badRequest.RemoteAddr = "127.0.0.1:55123"
	bad := httptest.NewRecorder()
	app.Handler().ServeHTTP(bad, badRequest)
	if bad.Code != http.StatusForbidden {
		t.Fatalf("non-local Host status=%d, want 403", bad.Code)
	}
}

func TestAPIEndToEndAndExcelExport(t *testing.T) {
	shop, _ := newFakeShopify(t)
	app, err := NewAppServer(URLGuard{AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}

	discoverBody, _ := json.Marshal(map[string]string{"store_url": shop.URL})
	discoverResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(discoverResponse, localRequest(http.MethodPost, "http://127.0.0.1:8787/api/discover", discoverBody))
	if discoverResponse.Code != http.StatusOK {
		t.Fatalf("discover: %d %s", discoverResponse.Code, discoverResponse.Body.String())
	}
	var metadata StoreMetadata
	if err := json.Unmarshal(discoverResponse.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}

	scanBody, _ := json.Marshal(ScanRequest{StoreURL: metadata.StoreURL, TitlePhrases: []string{"iPhone 16"}})
	scanResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(scanResponse, localRequest(http.MethodPost, "http://127.0.0.1:8787/api/scans", scanBody))
	if scanResponse.Code != http.StatusAccepted {
		t.Fatalf("scan start: %d %s", scanResponse.Code, scanResponse.Body.String())
	}

	deadline := time.Now().Add(8 * time.Second)
	var snapshot ScanSnapshot
	for time.Now().Before(deadline) {
		current := httptest.NewRecorder()
		app.Handler().ServeHTTP(current, localRequest(http.MethodGet, "http://127.0.0.1:8787/api/scans/current", nil))
		if err := json.Unmarshal(current.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Status != "running" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if snapshot.Status != "completed" || snapshot.Summary.Products != 1 || snapshot.Summary.Images != 2 || snapshot.Summary.Variants != 2 || snapshot.Summary.Errors != 1 {
		t.Fatalf("unexpected completed snapshot: status=%s summary=%#v error=%q", snapshot.Status, snapshot.Summary, snapshot.Error)
	}
	if len(snapshot.Products) != 1 || snapshot.Products[0].DescriptionText != "Điện thoại mới." || snapshot.Products[0].ImageCount != 2 {
		t.Fatalf("product detail missing from API snapshot: %#v", snapshot.Products)
	}

	export := httptest.NewRecorder()
	app.Handler().ServeHTTP(export, localRequest(http.MethodGet, "http://127.0.0.1:8787/api/scans/current/export.xlsx", nil))
	if export.Code != http.StatusOK || !bytes.HasPrefix(export.Body.Bytes(), []byte("PK")) {
		t.Fatalf("export: status=%d type=%s bytes=%d", export.Code, export.Header().Get("Content-Type"), export.Body.Len())
	}
}
