package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var webAssets embed.FS

type discoveryCache struct {
	crawler   *Crawler
	discovery storeDiscovery
	createdAt time.Time
}

type scanJob struct {
	mu     sync.RWMutex
	cancel context.CancelFunc
	data   ScanSnapshot
}

type AppServer struct {
	guard URLGuard
	mu    sync.RWMutex
	cache *discoveryCache
	job   *scanJob
	mux   *http.ServeMux
}

func NewAppServer(guard URLGuard) (*AppServer, error) {
	server := &AppServer{guard: guard, mux: http.NewServeMux()}
	staticFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		return nil, err
	}
	server.mux.HandleFunc("POST /api/discover", server.handleDiscover)
	server.mux.HandleFunc("POST /api/scans", server.handleStartScan)
	server.mux.HandleFunc("GET /api/scans/current", server.handleCurrentScan)
	server.mux.HandleFunc("POST /api/scans/current/cancel", server.handleCancelScan)
	server.mux.HandleFunc("GET /api/scans/current/export.xlsx", server.handleExport)
	server.mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(staticFS))))
	server.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		content, readErr := fs.ReadFile(staticFS, "index.html")
		if readErr != nil {
			http.Error(w, "Không tải được dashboard", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(content)
	})
	return server, nil
}

func (s *AppServer) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https: data:; style-src 'self'; script-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		if !isLocalRequest(r) {
			http.Error(w, "Dashboard chỉ chấp nhận request local", http.StatusForbidden)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	host = strings.Trim(host, "[]")
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return false
	}
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(remote)
	return ip != nil && ip.IsLoopback()
}

func (s *AppServer) handleDiscover(w http.ResponseWriter, r *http.Request) {
	var input struct {
		StoreURL string `json:"store_url"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	crawler, discovery, err := DiscoverStore(r.Context(), input.StoreURL, s.guard)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err)
		return
	}
	s.mu.Lock()
	s.cache = &discoveryCache{crawler: crawler, discovery: discovery, createdAt: time.Now()}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, discovery.Metadata)
}

func (s *AppServer) handleStartScan(w http.ResponseWriter, r *http.Request) {
	var request ScanRequest
	if err := decodeJSON(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	request.StoreURL = strings.TrimSpace(request.StoreURL)
	request.TitlePhrases = cleanPhrases(request.TitlePhrases)
	if request.StoreURL == "" {
		writeAPIError(w, http.StatusBadRequest, errors.New("thiếu URL store"))
		return
	}

	s.mu.RLock()
	cached := s.cache
	s.mu.RUnlock()
	if cached == nil || !strings.EqualFold(strings.TrimRight(cached.discovery.Metadata.StoreURL, "/"), strings.TrimRight(request.StoreURL, "/")) || time.Since(cached.createdAt) > 15*time.Minute {
		crawler, discovery, err := DiscoverStore(r.Context(), request.StoreURL, s.guard)
		if err != nil {
			writeAPIError(w, http.StatusBadGateway, err)
			return
		}
		cached = &discoveryCache{crawler: crawler, discovery: discovery, createdAt: time.Now()}
		s.mu.Lock()
		s.cache = cached
		s.mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	job := &scanJob{
		cancel: cancel,
		data: ScanSnapshot{
			ID: fmt.Sprintf("%d", now.UnixNano()), Status: "running", Store: cached.discovery.Metadata, Request: request,
			Progress: ScanProgress{StartedAt: now, Message: "Đang chuẩn bị phạm vi quét"},
			Products: []ProductSnapshot{}, Variants: []VariantSnapshot{}, Errors: []CrawlError{},
		},
	}
	s.mu.Lock()
	if s.job != nil {
		s.job.mu.RLock()
		oldCancel := s.job.cancel
		oldStatus := s.job.data.Status
		s.job.mu.RUnlock()
		if oldStatus == "running" && oldCancel != nil {
			oldCancel()
		}
	}
	s.job = job
	s.mu.Unlock()

	go s.runScan(ctx, job, cached)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": job.data.ID, "status": "running"})
}

func (s *AppServer) runScan(ctx context.Context, job *scanJob, cached *discoveryCache) {
	hooks := scanHooks{
		SetProgress: func(discovered, read, matched int, message string) {
			job.mu.Lock()
			job.data.Progress.Discovered = discovered
			job.data.Progress.Read = read
			job.data.Progress.Matched = matched
			job.data.Progress.Message = message
			job.mu.Unlock()
		},
		AddProduct: func(product ProductSnapshot, variants []VariantSnapshot) {
			job.mu.Lock()
			job.data.Products = append(job.data.Products, product)
			job.data.Variants = append(job.data.Variants, variants...)
			job.mu.Unlock()
		},
		AddError: func(crawlErr CrawlError) {
			job.mu.Lock()
			job.data.Errors = append(job.data.Errors, crawlErr)
			job.data.Progress.Errors++
			job.mu.Unlock()
		},
	}
	job.mu.RLock()
	request := job.data.Request
	job.mu.RUnlock()
	err := cached.crawler.Scan(ctx, cached.discovery, request, hooks)
	finished := time.Now()
	job.mu.Lock()
	defer job.mu.Unlock()
	job.data.Progress.FinishedAt = &finished
	job.data.Summary = summarize(job.data.Products, job.data.Variants, job.data.Errors)
	sort.Slice(job.data.Products, func(i, j int) bool {
		return foldText(job.data.Products[i].Title) < foldText(job.data.Products[j].Title)
	})
	sort.Slice(job.data.Variants, func(i, j int) bool {
		left, right := foldText(job.data.Variants[i].ProductTitle), foldText(job.data.Variants[j].ProductTitle)
		if left == right {
			return foldText(job.data.Variants[i].VariantTitle) < foldText(job.data.Variants[j].VariantTitle)
		}
		return left < right
	})
	if errors.Is(err, context.Canceled) {
		job.data.Status = "cancelled"
		job.data.Progress.Message = "Đã hủy theo yêu cầu"
	} else if err != nil {
		job.data.Status = "failed"
		job.data.Error = err.Error()
		job.data.Progress.Message = "Quét thất bại"
	} else {
		job.data.Status = "completed"
		job.data.Progress.Message = fmt.Sprintf("Hoàn tất: %d product, %d variant", job.data.Summary.Products, job.data.Summary.Variants)
	}
}

func summarize(products []ProductSnapshot, variants []VariantSnapshot, crawlErrors []CrawlError) ScanSummary {
	summary := ScanSummary{Products: len(products), Variants: len(variants)}
	for _, product := range products {
		summary.Images += product.ImageCount
	}
	prices := make([]int64, 0, len(variants))
	var total float64
	for _, variant := range variants {
		prices = append(prices, variant.PriceMinor)
		total += float64(variant.PriceMinor)
		if variant.Available == nil {
			summary.UnknownAvailability++
		} else if *variant.Available {
			summary.Available++
		} else {
			summary.Unavailable++
		}
		if variant.DiscountMinor > 0 {
			summary.Discounted++
		}
	}
	for _, crawlErr := range crawlErrors {
		if crawlErr.Warning {
			summary.Warnings++
		} else {
			summary.Errors++
		}
	}
	if len(prices) > 0 {
		sort.Slice(prices, func(i, j int) bool { return prices[i] < prices[j] })
		summary.MinPriceMinor = prices[0]
		summary.MaxPriceMinor = prices[len(prices)-1]
		summary.AveragePriceMinor = total / float64(len(prices))
		middle := len(prices) / 2
		if len(prices)%2 == 0 {
			summary.MedianPriceMinor = float64(prices[middle-1]+prices[middle]) / 2
		} else {
			summary.MedianPriceMinor = float64(prices[middle])
		}
	}
	return summary
}

func (s *AppServer) handleCurrentScan(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	job := s.job
	s.mu.RUnlock()
	if job == nil {
		writeJSON(w, http.StatusOK, ScanSnapshot{Status: "idle", Products: []ProductSnapshot{}, Variants: []VariantSnapshot{}, Errors: []CrawlError{}})
		return
	}
	writeJSON(w, http.StatusOK, job.snapshot())
}

func (s *AppServer) handleCancelScan(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	job := s.job
	s.mu.RUnlock()
	if job == nil {
		writeAPIError(w, http.StatusConflict, errors.New("không có scan đang chạy"))
		return
	}
	job.mu.RLock()
	status, cancel := job.data.Status, job.cancel
	job.mu.RUnlock()
	if status != "running" || cancel == nil {
		writeAPIError(w, http.StatusConflict, errors.New("scan hiện tại không còn chạy"))
		return
	}
	cancel()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelling"})
}

func (s *AppServer) handleExport(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	job := s.job
	s.mu.RUnlock()
	if job == nil {
		writeAPIError(w, http.StatusConflict, errors.New("chưa có snapshot để xuất"))
		return
	}
	snapshot := job.snapshot()
	if snapshot.Status != "completed" && snapshot.Status != "cancelled" {
		writeAPIError(w, http.StatusConflict, errors.New("hãy chờ scan hoàn tất hoặc hủy scan trước khi xuất Excel"))
		return
	}
	content, filename, err := BuildWorkbook(snapshot)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
	w.Write(content)
}

func (j *scanJob) snapshot() ScanSnapshot {
	j.mu.RLock()
	defer j.mu.RUnlock()
	copyOf := j.data
	copyOf.Products = append([]ProductSnapshot(nil), j.data.Products...)
	copyOf.Variants = append([]VariantSnapshot(nil), j.data.Variants...)
	copyOf.Errors = append([]CrawlError(nil), j.data.Errors...)
	if copyOf.Status == "running" {
		copyOf.Summary = summarize(copyOf.Products, copyOf.Variants, copyOf.Errors)
	}
	return copyOf
}

func decodeJSON(r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("JSON request không hợp lệ: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
