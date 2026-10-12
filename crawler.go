package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"math/rand/v2"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	requestTimeout  = 15 * time.Second
	maxRetries      = 3
	maxSitemapSize  = 12 << 20
	maxHTMLSize     = 8 << 20
	maxJSONSize     = 24 << 20
	maxBulkJSONSize = 64 << 20
)

type Crawler struct {
	guard           URLGuard
	client          *http.Client
	baseURL         *url.URL
	localeRoot      string
	paceMu          sync.Mutex
	nextRequest     time.Time
	requestInterval time.Duration
}

type storeDiscovery struct {
	Metadata    StoreMetadata
	ProductURLs []string
	Collections []CollectionRef
}

type httpFailure struct {
	URL      string
	Status   int
	Attempts int
	Message  string
}

func (e *httpFailure) Error() string { return e.Message }

type scanHooks struct {
	SetProgress func(discovered, read, matched int, message string)
	AddProduct  func(ProductSnapshot, []VariantSnapshot)
	AddError    func(CrawlError)
}

type productScanResult struct {
	handle   string
	product  ProductSnapshot
	variants []VariantSnapshot
	err      *CrawlError
}

var (
	routesRootExpression = regexp.MustCompile(`(?i)Shopify\.routes\.root\s*=\s*["']([^"']+)["']`)
	titleExpression      = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	hrefExpression       = regexp.MustCompile(`(?is)href\s*=\s*["']([^"']+)["']`)
)

func DiscoverStore(ctx context.Context, rawURL string, guard URLGuard) (*Crawler, storeDiscovery, error) {
	normalized, err := guard.NormalizeStoreURL(rawURL)
	if err != nil {
		return nil, storeDiscovery{}, err
	}
	client, err := guard.NewHTTPClient(requestTimeout)
	if err != nil {
		return nil, storeDiscovery{}, err
	}

	homeBody, finalURL, _, err := fetchWithRetry(ctx, client, guard, normalized, nil, "text/html,application/xhtml+xml", maxHTMLSize)
	if err != nil {
		return nil, storeDiscovery{}, fmt.Errorf("không mở được store: %w", err)
	}
	homeLower := strings.ToLower(string(homeBody))
	if strings.Contains(homeLower, "shopify-password") || strings.Contains(homeLower, "password-page") || strings.Contains(finalURL.Path, "/password") {
		return nil, storeDiscovery{}, errors.New("store đang bật trang mật khẩu; dữ liệu công khai không thể được quét")
	}
	if isActiveBotChallenge(finalURL, homeBody) {
		return nil, storeDiscovery{}, errors.New("store đang yêu cầu captcha/chống bot")
	}

	base := &url.URL{Scheme: finalURL.Scheme, Host: finalURL.Host, Path: "/"}
	interval := 300 * time.Millisecond
	if guard.AllowPrivate {
		interval = 0
	}
	crawler := &Crawler{guard: guard, client: client, baseURL: base, localeRoot: detectLocaleRoot(homeBody), requestInterval: interval}
	productURLs, collectionURLs, sitemapErrors := crawler.discoverSitemaps(ctx)
	shopifyEvidence := strings.Contains(homeLower, "cdn.shopify.com") || strings.Contains(homeLower, "shopify.theme") || strings.Contains(homeLower, "shopify-section") || len(productURLs) > 0 || len(collectionURLs) > 0
	if !shopifyEvidence {
		return nil, storeDiscovery{}, errors.New("không nhận diện được Shopify Online Store công khai tại URL này")
	}

	warnings := append([]string(nil), sitemapErrors...)
	currency := "UNKNOWN"
	cartURL := crawler.resolveLocale("cart.js")
	if body, _, _, fetchErr := crawler.fetch(ctx, cartURL, "application/json", 2<<20); fetchErr == nil {
		var cart struct {
			Currency string `json:"currency"`
		}
		if json.Unmarshal(body, &cart) == nil && cart.Currency != "" {
			currency = strings.ToUpper(cart.Currency)
		} else {
			warnings = append(warnings, "Không đọc được currency từ cart.js")
		}
	} else {
		warnings = append(warnings, "Không đọc được currency hiện tại: "+fetchErr.Error())
	}

	collections := make([]CollectionRef, 0, len(collectionURLs))
	seenCollections := map[string]bool{}
	for _, raw := range collectionURLs {
		u, parseErr := url.Parse(raw)
		if parseErr != nil || !sameHostname(base, u) {
			continue
		}
		handle := collectionHandle(u.Path)
		if handle == "" || seenCollections[handle] {
			continue
		}
		seenCollections[handle] = true
		collections = append(collections, CollectionRef{Title: humanizeHandle(handle), Handle: handle, URL: u.String()})
	}
	sort.Slice(collections, func(i, j int) bool { return foldText(collections[i].Title) < foldText(collections[j].Title) })
	sort.Strings(productURLs)

	name := strings.TrimPrefix(base.Hostname(), "www.")
	if match := titleExpression.FindSubmatch(homeBody); len(match) > 1 {
		if candidate := plainText(string(match[1])); candidate != "" {
			name = candidate
		}
	}
	storeURL := base.String()
	if crawler.localeRoot != "/" {
		localized := *base
		localized.Path = crawler.localeRoot
		storeURL = localized.String()
	}
	metadata := StoreMetadata{
		StoreURL: storeURL, Name: name, LocaleRoot: crawler.localeRoot, Currency: currency,
		ProductCount: len(productURLs), Collections: collections, Warnings: uniqueSorted(warnings),
	}
	return crawler, storeDiscovery{Metadata: metadata, ProductURLs: productURLs, Collections: collections}, nil
}

// Shopify storefronts normally embed captcha-bootstrap and hCaptcha JavaScript
// to protect account/contact forms. Those scripts do not mean the visitor is
// currently blocked. Only classify the response as a challenge when the final
// route or the visible page title is a verification page.
func isActiveBotChallenge(finalURL *url.URL, body []byte) bool {
	pathLower := ""
	if finalURL != nil {
		pathLower = strings.ToLower(finalURL.Path)
	}
	for _, marker := range []string{"/challenge", "/checkpoint", "/captcha"} {
		if strings.Contains(pathLower, marker) {
			return true
		}
	}

	bodyLower := strings.ToLower(string(body))
	title := ""
	if match := titleExpression.FindSubmatch(body); len(match) > 1 {
		title = foldText(plainText(string(match[1])))
	}
	verificationTitle := strings.Contains(title, "verify you are human") ||
		strings.Contains(title, "are you a human") ||
		strings.Contains(title, "security check") ||
		strings.Contains(title, "captcha") ||
		strings.Contains(title, "just a moment")
	challengeMarkup := strings.Contains(bodyLower, "hcaptcha") ||
		strings.Contains(bodyLower, "g-recaptcha") ||
		strings.Contains(bodyLower, "cf-chl-") ||
		strings.Contains(bodyLower, "challenge-form")
	return verificationTitle && challengeMarkup
}

func detectLocaleRoot(body []byte) string {
	match := routesRootExpression.FindSubmatch(body)
	if len(match) < 2 {
		return "/"
	}
	root := strings.TrimSpace(string(match[1]))
	if !strings.HasPrefix(root, "/") {
		root = "/" + root
	}
	if !strings.HasSuffix(root, "/") {
		root += "/"
	}
	if strings.Contains(root, "..") {
		return "/"
	}
	return root
}

func (c *Crawler) resolveLocale(relative string) *url.URL {
	u := *c.baseURL
	u.Path = path.Clean(c.localeRoot + "/" + strings.TrimPrefix(relative, "/"))
	if strings.HasSuffix(relative, "/") && !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return &u
}

func (c *Crawler) fetch(ctx context.Context, target *url.URL, accept string, maxBytes int64) ([]byte, *url.URL, int, error) {
	if !sameHostname(c.baseURL, target) {
		return nil, target, 0, &httpFailure{URL: target.String(), Message: "URL nằm ngoài store đã chọn"}
	}
	if err := c.waitForRequestTurn(ctx); err != nil {
		return nil, target, 0, err
	}
	return fetchWithRetry(ctx, c.client, c.guard, target, c.baseURL, accept, maxBytes)
}

func (c *Crawler) waitForRequestTurn(ctx context.Context) error {
	if c.requestInterval <= 0 {
		return nil
	}
	c.paceMu.Lock()
	now := time.Now()
	scheduled := now
	if c.nextRequest.After(now) {
		scheduled = c.nextRequest
	}
	c.nextRequest = scheduled.Add(c.requestInterval)
	c.paceMu.Unlock()
	if !scheduled.After(now) {
		return nil
	}
	timer := time.NewTimer(time.Until(scheduled))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func fetchWithRetry(ctx context.Context, client *http.Client, guard URLGuard, target, expectedBase *url.URL, accept string, maxBytes int64) ([]byte, *url.URL, int, error) {
	var lastStatus int
	var lastMessage string
	for attempt := 1; attempt <= maxRetries+1; attempt++ {
		if err := guard.ValidateURL(ctx, target); err != nil {
			return nil, target, attempt, &httpFailure{URL: target.String(), Attempts: attempt, Message: err.Error()}
		}
		if expectedBase != nil && !sameHostname(expectedBase, target) {
			return nil, target, attempt, &httpFailure{URL: target.String(), Attempts: attempt, Message: "URL nằm ngoài hostname của store"}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, target, attempt, err
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", "ShopifyPriceChecker/"+appVersion+" (local store audit)")
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, target, attempt, ctx.Err()
			}
			lastMessage = err.Error()
			if attempt <= maxRetries {
				if !waitRetry(ctx, retryDelay(attempt, "")) {
					return nil, target, attempt, ctx.Err()
				}
				continue
			}
			return nil, target, attempt, &httpFailure{URL: target.String(), Attempts: attempt, Message: lastMessage}
		}
		finalURL := resp.Request.URL
		if expectedBase != nil && !sameHostname(expectedBase, finalURL) {
			resp.Body.Close()
			return nil, finalURL, attempt, &httpFailure{URL: finalURL.String(), Status: resp.StatusCode, Attempts: attempt, Message: "redirect ra ngoài hostname của store"}
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		resp.Body.Close()
		lastStatus = resp.StatusCode
		if int64(len(body)) > maxBytes {
			return nil, finalURL, attempt, &httpFailure{URL: finalURL.String(), Status: resp.StatusCode, Attempts: attempt, Message: "phản hồi vượt giới hạn kích thước an toàn"}
		}
		if readErr != nil {
			lastMessage = readErr.Error()
		} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, finalURL, attempt, nil
		} else {
			lastMessage = fmt.Sprintf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		}
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt <= maxRetries {
			if !waitRetry(ctx, retryDelay(attempt, resp.Header.Get("Retry-After"))) {
				return nil, finalURL, attempt, ctx.Err()
			}
			continue
		}
		return nil, finalURL, attempt, &httpFailure{URL: finalURL.String(), Status: resp.StatusCode, Attempts: attempt, Message: lastMessage}
	}
	return nil, target, maxRetries + 1, &httpFailure{URL: target.String(), Status: lastStatus, Attempts: maxRetries + 1, Message: lastMessage}
}

func retryDelay(attempt int, retryAfter string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds > 0 {
		if seconds > 30 {
			seconds = 30
		}
		return time.Duration(seconds) * time.Second
	}
	delay := time.Duration(1<<min(attempt-1, 4)) * time.Second
	return delay + time.Duration(rand.IntN(350))*time.Millisecond
}

func waitRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type sitemapIndexXML struct {
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
}

type urlSetXML struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

func (c *Crawler) discoverSitemaps(ctx context.Context) ([]string, []string, []string) {
	root := *c.baseURL
	root.Path = "/sitemap.xml"
	queue := []string{root.String()}
	visited := map[string]bool{}
	products := map[string]bool{}
	collections := map[string]bool{}
	var warnings []string
	for len(queue) > 0 && len(visited) < 100 {
		current := queue[0]
		queue = queue[1:]
		if visited[current] {
			continue
		}
		visited[current] = true
		u, err := url.Parse(current)
		if err != nil || !sameHostname(c.baseURL, u) {
			continue
		}
		body, _, _, fetchErr := c.fetch(ctx, u, "application/xml,text/xml", maxSitemapSize)
		if fetchErr != nil {
			warnings = append(warnings, "Không đọc được sitemap: "+current+" ("+fetchErr.Error()+")")
			continue
		}
		var index sitemapIndexXML
		if xml.Unmarshal(body, &index) == nil {
			for _, item := range index.Sitemaps {
				if child, parseErr := url.Parse(strings.TrimSpace(item.Loc)); parseErr == nil && sameHostname(c.baseURL, child) {
					queue = append(queue, child.String())
				}
			}
		}
		var urlSet urlSetXML
		if err := xml.Unmarshal(body, &urlSet); err != nil {
			warnings = append(warnings, "Sitemap XML không hợp lệ: "+current)
			continue
		}
		for _, item := range urlSet.URLs {
			candidate, parseErr := url.Parse(strings.TrimSpace(item.Loc))
			if parseErr != nil || !sameHostname(c.baseURL, candidate) {
				continue
			}
			if productHandle(candidate.Path) != "" {
				products[candidate.String()] = true
			} else if collectionHandle(candidate.Path) != "" {
				collections[candidate.String()] = true
			}
		}
	}
	return mapKeys(products), mapKeys(collections), warnings
}

func mapKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func productHandle(rawPath string) string {
	segments := strings.Split(strings.Trim(rawPath, "/"), "/")
	for index, segment := range segments {
		if segment == "products" && index+1 < len(segments) {
			handle, _ := url.PathUnescape(strings.TrimSuffix(segments[index+1], ".js"))
			return strings.TrimSpace(handle)
		}
	}
	return ""
}

func collectionHandle(rawPath string) string {
	segments := strings.Split(strings.Trim(rawPath, "/"), "/")
	for index, segment := range segments {
		if segment == "collections" && index+1 < len(segments) && segments[index+1] != "all" && segments[index+1] != "types" && segments[index+1] != "vendors" {
			handle, _ := url.PathUnescape(segments[index+1])
			return strings.TrimSpace(handle)
		}
	}
	return ""
}

func humanizeHandle(handle string) string {
	words := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(handle))
	for index, word := range words {
		if word != "" {
			words[index] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

func (c *Crawler) Scan(ctx context.Context, discovery storeDiscovery, request ScanRequest, hooks scanHooks) error {
	phrases := cleanPhrases(request.TitlePhrases)
	productByHandle := map[string]string{}
	for _, raw := range discovery.ProductURLs {
		if u, err := url.Parse(raw); err == nil {
			if handle := productHandle(u.Path); handle != "" {
				productByHandle[handle] = raw
			}
		}
	}
	selectedCollections, selectionErrors := resolveSelectedCollections(discovery.Collections, request.Collections)
	for _, crawlErr := range selectionErrors {
		hooks.AddError(crawlErr)
	}

	// Only resolve collection membership when a collection filter is active.
	// Mapping every collection before a full-store scan creates dozens of
	// redundant requests and can trigger Shopify abuse prevention.
	membership := map[string][]string{}
	successfulCollections := 0
	if len(request.Collections) > 0 {
		membership, successfulCollections = c.mapCollectionMembership(ctx, selectedCollections, hooks)
	}
	candidates := productByHandle
	if len(request.Collections) > 0 {
		candidates = map[string]string{}
		if successfulCollections == 0 {
			for handle, raw := range productByHandle {
				candidates[handle] = raw
			}
			hooks.AddError(CrawlError{Stage: "collection", Attempts: 1, Warning: true, Message: "Không đọc được membership của collection; đã fallback sang toàn bộ sitemap. Hãy kiểm tra cảnh báo trước khi dùng kết quả."})
		} else {
			for handle := range membership {
				if raw, ok := productByHandle[handle]; ok {
					candidates[handle] = raw
				}
			}
		}
	}

	handles := make([]string, 0, len(candidates))
	for handle := range candidates {
		handles = append(handles, handle)
	}
	sort.Strings(handles)
	hooks.SetProgress(len(handles), 0, 0, "Đang đọc dữ liệu product và variant")

	read, matched := 0, 0
	consume := func(result productScanResult) {
		read++
		if result.err != nil {
			hooks.AddError(*result.err)
		} else if titleMatches(result.product.Title, phrases) {
			matched++
			hooks.AddProduct(result.product, result.variants)
			if result.product.VariantLimitWarning {
				hooks.AddError(CrawlError{Stage: "variant_limit", URL: result.product.URL, Attempts: 1, Warning: true, Message: "Product trả về đúng 250 variants; Shopify có thể đã cắt bớt dữ liệu công khai."})
			}
		}
		hooks.SetProgress(len(handles), read, matched, fmt.Sprintf("Đã đọc %d/%d product", read, len(handles)))
	}

	// Prefer Shopify's paginated public catalog. A 576-product store normally
	// needs only three bulk calls. Any product absent from the bulk response is
	// fetched individually below as a compatibility fallback.
	bulkResults, processed := c.fetchBulkProducts(ctx, candidates, membership)
	for _, result := range bulkResults {
		consume(result)
	}
	remaining := make([]string, 0, len(handles)-len(processed))
	for _, handle := range handles {
		if !processed[handle] {
			remaining = append(remaining, handle)
		}
	}
	if len(remaining) == 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}

	work := make(chan string)
	results := make(chan productScanResult)
	var workers sync.WaitGroup
	workerCount := min(2, max(1, len(remaining)))
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for handle := range work {
				product, variants, crawlErr := c.fetchProduct(ctx, handle, membership[handle])
				result := productScanResult{handle: handle, product: product, variants: variants, err: crawlErr}
				select {
				case results <- result:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(work)
		for _, handle := range remaining {
			select {
			case work <- handle:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	for result := range results {
		consume(result)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func resolveSelectedCollections(all []CollectionRef, selected []string) ([]CollectionRef, []CrawlError) {
	if len(selected) == 0 {
		return nil, nil
	}
	byHandle := map[string]CollectionRef{}
	for _, collection := range all {
		byHandle[collection.Handle] = collection
	}
	seen := map[string]bool{}
	var result []CollectionRef
	var errs []CrawlError
	for _, value := range selected {
		handle := strings.TrimSpace(value)
		if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
			handle = collectionHandle(parsed.Path)
		}
		if collection, ok := byHandle[handle]; ok && !seen[handle] {
			seen[handle] = true
			result = append(result, collection)
		} else if handle != "" && !seen[handle] {
			seen[handle] = true
			errs = append(errs, CrawlError{Stage: "collection", URL: value, Attempts: 1, Warning: true, Message: "Collection không có trong sitemap: " + handle})
		}
	}
	return result, errs
}

func (c *Crawler) mapCollectionMembership(ctx context.Context, collections []CollectionRef, hooks scanHooks) (map[string][]string, int) {
	type result struct {
		collection CollectionRef
		handles    []string
		err        error
		attempts   int
		status     int
	}
	membership := map[string][]string{}
	if len(collections) == 0 {
		return membership, 0
	}
	work := make(chan CollectionRef)
	results := make(chan result)
	var workers sync.WaitGroup
	for range min(2, len(collections)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for collection := range work {
				handles, attempts, status, err := c.collectionProductHandles(ctx, collection)
				select {
				case results <- result{collection: collection, handles: handles, err: err, attempts: attempts, status: status}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(work)
		for _, collection := range collections {
			select {
			case work <- collection:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { workers.Wait(); close(results) }()
	successful := 0
	for item := range results {
		if item.err != nil {
			hooks.AddError(CrawlError{Stage: "collection", URL: item.collection.URL, Status: item.status, Attempts: item.attempts, Warning: true, Message: item.err.Error()})
			continue
		}
		successful++
		for _, handle := range item.handles {
			membership[handle] = append(membership[handle], item.collection.Title)
		}
	}
	for handle, names := range membership {
		membership[handle] = uniqueSorted(names)
	}
	return membership, successful
}

func (c *Crawler) collectionProductHandles(ctx context.Context, collection CollectionRef) ([]string, int, int, error) {
	jsonHandles, attempts, status, jsonErr := c.collectionProductsJSON(ctx, collection.Handle)
	if jsonErr == nil {
		return jsonHandles, attempts, status, nil
	}
	htmlHandles, htmlAttempts, htmlStatus, htmlErr := c.collectionProductsHTML(ctx, collection)
	if htmlErr == nil {
		return htmlHandles, attempts + htmlAttempts, htmlStatus, nil
	}
	return nil, attempts + htmlAttempts, htmlStatus, fmt.Errorf("không đọc được collection JSON (%v) hoặc HTML (%v)", jsonErr, htmlErr)
}

func (c *Crawler) collectionProductsJSON(ctx context.Context, handle string) ([]string, int, int, error) {
	seen := map[string]bool{}
	totalAttempts, lastStatus := 0, 0
	for pageNumber := 1; pageNumber <= 100; pageNumber++ {
		target := c.resolveLocale("collections/" + url.PathEscape(handle) + "/products.json")
		query := target.Query()
		query.Set("limit", "250")
		query.Set("page", strconv.Itoa(pageNumber))
		target.RawQuery = query.Encode()
		body, _, attempts, err := c.fetch(ctx, target, "application/json", maxJSONSize)
		totalAttempts += attempts
		if failure := new(httpFailure); errors.As(err, &failure) {
			lastStatus = failure.Status
		}
		if err != nil {
			return nil, totalAttempts, lastStatus, err
		}
		var payload struct {
			Products []struct {
				Handle string `json:"handle"`
				URL    string `json:"url"`
			} `json:"products"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, totalAttempts, lastStatus, errors.New("JSON collection không hợp lệ")
		}
		newCount := 0
		for _, product := range payload.Products {
			h := strings.TrimSpace(product.Handle)
			if h == "" {
				h = productHandle(product.URL)
			}
			if h != "" && !seen[h] {
				seen[h] = true
				newCount++
			}
		}
		if len(payload.Products) < 250 || newCount == 0 {
			break
		}
	}
	return mapKeys(seen), totalAttempts, lastStatus, nil
}

func (c *Crawler) collectionProductsHTML(ctx context.Context, collection CollectionRef) ([]string, int, int, error) {
	start, err := url.Parse(collection.URL)
	if err != nil || !sameHostname(c.baseURL, start) {
		return nil, 0, 0, errors.New("URL collection không hợp lệ")
	}
	seenHandles := map[string]bool{}
	seenPages := map[string]bool{}
	queue := []*url.URL{start}
	totalAttempts, lastStatus := 0, 0
	for len(queue) > 0 && len(seenPages) < 100 {
		current := queue[0]
		queue = queue[1:]
		if seenPages[current.String()] {
			continue
		}
		seenPages[current.String()] = true
		body, _, attempts, fetchErr := c.fetch(ctx, current, "text/html", maxHTMLSize)
		totalAttempts += attempts
		if failure := new(httpFailure); errors.As(fetchErr, &failure) {
			lastStatus = failure.Status
		}
		if fetchErr != nil {
			if len(seenHandles) == 0 {
				return nil, totalAttempts, lastStatus, fetchErr
			}
			break
		}
		newOnPage := 0
		for _, match := range hrefExpression.FindAllSubmatch(body, -1) {
			if len(match) < 2 {
				continue
			}
			href := strings.TrimSpace(strings.ReplaceAll(string(match[1]), "&amp;", "&"))
			linked, parseErr := current.Parse(href)
			if parseErr != nil || !sameHostname(c.baseURL, linked) {
				continue
			}
			if handle := productHandle(linked.Path); handle != "" {
				if !seenHandles[handle] {
					seenHandles[handle] = true
					newOnPage++
				}
				continue
			}
			if collectionHandle(linked.Path) == collection.Handle && hasPageParameter(linked.Query()) && !seenPages[linked.String()] {
				queue = append(queue, linked)
			}
		}
		if len(queue) == 0 && newOnPage > 0 {
			next := *start
			query := next.Query()
			query.Set("page", strconv.Itoa(len(seenPages)+1))
			next.RawQuery = query.Encode()
			if !seenPages[next.String()] {
				queue = append(queue, &next)
			}
		}
		if newOnPage == 0 && len(seenPages) > 1 {
			break
		}
	}
	if len(seenHandles) == 0 {
		return nil, totalAttempts, lastStatus, errors.New("không tìm thấy product link trong trang collection")
	}
	return mapKeys(seenHandles), totalAttempts, lastStatus, nil
}

func hasPageParameter(values url.Values) bool {
	for key := range values {
		if strings.HasPrefix(strings.ToLower(key), "page") {
			return true
		}
	}
	return false
}

type stringList []string

func (s *stringList) UnmarshalJSON(data []byte) error {
	var list []string
	if json.Unmarshal(data, &list) == nil {
		*s = list
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	if strings.TrimSpace(single) == "" {
		*s = nil
	} else {
		*s = strings.Split(single, ",")
	}
	return nil
}

type ajaxProduct struct {
	ID            json.RawMessage   `json:"id"`
	Title         string            `json:"title"`
	Handle        string            `json:"handle"`
	Description   string            `json:"description"`
	BodyHTML      string            `json:"body_html"`
	Vendor        string            `json:"vendor"`
	Type          string            `json:"type"`
	ProductType   string            `json:"product_type"`
	Tags          stringList        `json:"tags"`
	PublishedAt   string            `json:"published_at"`
	CreatedAt     string            `json:"created_at"`
	Available     *bool             `json:"available"`
	FeaturedImage json.RawMessage   `json:"featured_image"`
	Image         json.RawMessage   `json:"image"`
	Images        []json.RawMessage `json:"images"`
	Options       []json.RawMessage `json:"options"`
	Variants      []ajaxVariant     `json:"variants"`
}

type ajaxVariant struct {
	ID             json.RawMessage `json:"id"`
	Title          string          `json:"title"`
	Option1        *string         `json:"option1"`
	Option2        *string         `json:"option2"`
	Option3        *string         `json:"option3"`
	SKU            *string         `json:"sku"`
	Barcode        *string         `json:"barcode"`
	Price          json.RawMessage `json:"price"`
	CompareAtPrice json.RawMessage `json:"compare_at_price"`
	Available      *bool           `json:"available"`
	FeaturedImage  json.RawMessage `json:"featured_image"`
}

func (c *Crawler) fetchBulkProducts(ctx context.Context, candidates map[string]string, membership map[string][]string) ([]productScanResult, map[string]bool) {
	results := make([]productScanResult, 0, len(candidates))
	processed := map[string]bool{}
	if len(candidates) == 0 {
		return results, processed
	}
	for pageNumber := 1; pageNumber <= 100; pageNumber++ {
		target := c.resolveLocale("products.json")
		query := target.Query()
		query.Set("limit", "250")
		query.Set("page", strconv.Itoa(pageNumber))
		target.RawQuery = query.Encode()
		body, _, _, err := c.fetch(ctx, target, "application/json", maxBulkJSONSize)
		if err != nil {
			break
		}
		var payload struct {
			Products []ajaxProduct `json:"products"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			break
		}
		if len(payload.Products) == 0 {
			break
		}
		for _, raw := range payload.Products {
			handle := strings.TrimSpace(raw.Handle)
			if handle == "" || processed[handle] {
				continue
			}
			if _, wanted := candidates[handle]; !wanted {
				continue
			}
			product, variants, normalizeErr := c.normalizeProduct(raw, handle, membership[handle])
			if normalizeErr != nil {
				// Leave the handle unprocessed so the locale-aware product.js
				// fallback can recover richer or differently shaped data.
				continue
			}
			processed[handle] = true
			results = append(results, productScanResult{handle: handle, product: product, variants: variants})
		}
		if len(payload.Products) < 250 || len(processed) == len(candidates) {
			break
		}
	}
	return results, processed
}

func (c *Crawler) fetchProduct(ctx context.Context, handle string, collections []string) (ProductSnapshot, []VariantSnapshot, *CrawlError) {
	target := c.resolveLocale("products/" + url.PathEscape(handle) + ".js")
	body, _, attempts, err := c.fetch(ctx, target, "application/json", maxJSONSize)
	if err != nil {
		return ProductSnapshot{}, nil, crawlErrorFrom("product", target.String(), attempts, err)
	}
	var raw ajaxProduct
	if err := json.Unmarshal(body, &raw); err != nil {
		return ProductSnapshot{}, nil, &CrawlError{Stage: "product", URL: target.String(), Attempts: attempts, Message: "JSON product không hợp lệ: " + err.Error()}
	}
	product, variants, normalizeErr := c.normalizeProduct(raw, handle, collections)
	if normalizeErr != nil {
		return ProductSnapshot{}, nil, &CrawlError{Stage: "product", URL: target.String(), Attempts: attempts, Message: normalizeErr.Error()}
	}
	return product, variants, nil
}

func (c *Crawler) normalizeProduct(raw ajaxProduct, handle string, collections []string) (ProductSnapshot, []VariantSnapshot, error) {
	if raw.Handle == "" {
		raw.Handle = handle
	}
	productURL := c.resolveLocale("products/" + url.PathEscape(raw.Handle)).String()
	images := normalizeProductImages(raw.Images, raw.FeaturedImage, raw.Image)
	imageURL := featuredProductImage(images)
	productType := strings.TrimSpace(raw.Type)
	if productType == "" {
		productType = strings.TrimSpace(raw.ProductType)
	}
	descriptionHTML := strings.TrimSpace(raw.Description)
	if descriptionHTML == "" {
		descriptionHTML = strings.TrimSpace(raw.BodyHTML)
	}
	product := ProductSnapshot{
		ProductID: idString(raw.ID), Title: strings.TrimSpace(raw.Title), Handle: raw.Handle,
		DescriptionHTML: descriptionHTML, DescriptionText: plainText(descriptionHTML),
		Vendor: strings.TrimSpace(raw.Vendor), ProductType: productType, Tags: uniqueSorted(raw.Tags),
		Collections: uniqueSorted(collections), Options: productOptionNames(raw.Options),
		PublishedAt: strings.TrimSpace(raw.PublishedAt), CreatedAt: strings.TrimSpace(raw.CreatedAt), Available: raw.Available,
		URL: productURL, ImageURL: imageURL, Images: images, ImageCount: len(images),
		VariantCount: len(raw.Variants), VariantLimitWarning: len(raw.Variants) == 250,
	}
	scannedAt := time.Now()
	variants := make([]VariantSnapshot, 0, len(raw.Variants))
	for index, variant := range raw.Variants {
		price, priceErr := moneyMinor(variant.Price)
		if priceErr != nil {
			return ProductSnapshot{}, nil, fmt.Errorf("giá variant %d không hợp lệ", index+1)
		}
		compareAt, compareErr := optionalMoneyMinor(variant.CompareAtPrice)
		if compareErr != nil {
			return ProductSnapshot{}, nil, fmt.Errorf("compare-at price variant %d không hợp lệ", index+1)
		}
		discount := int64(0)
		var discountPct *float64
		if compareAt != nil && *compareAt > price && *compareAt > 0 {
			discount = *compareAt - price
			value := float64(discount) / float64(*compareAt)
			discountPct = &value
		}
		variantID := idString(variant.ID)
		variantImage := parsedImageURL(variant.FeaturedImage)
		if variantImage == "" {
			variantImage = imageForVariant(images, variantID)
		}
		if variantImage == "" {
			variantImage = imageURL
		}
		entry := VariantSnapshot{
			ProductID: product.ProductID, VariantID: variantID, ProductTitle: product.Title,
			VariantTitle: strings.TrimSpace(variant.Title), Option1: pointerString(variant.Option1), Option2: pointerString(variant.Option2), Option3: pointerString(variant.Option3),
			SKU: pointerString(variant.SKU), Barcode: pointerString(variant.Barcode), Vendor: product.Vendor, ProductType: product.ProductType,
			Tags: product.Tags, Collections: product.Collections, PriceMinor: price, CompareAtMinor: compareAt,
			DiscountMinor: discount, DiscountPct: discountPct, Available: variant.Available,
			ImageURL: variantImage, ProductURL: productURL, ScannedAt: scannedAt,
		}
		variants = append(variants, entry)
		if index == 0 || price < product.MinPriceMinor {
			product.MinPriceMinor = price
		}
		if index == 0 || price > product.MaxPriceMinor {
			product.MaxPriceMinor = price
		}
		if variant.Available != nil && *variant.Available {
			product.AvailableCount++
		}
	}
	if product.Available == nil {
		knownAvailability := false
		available := false
		for _, variant := range variants {
			if variant.Available != nil {
				knownAvailability = true
				available = available || *variant.Available
			}
		}
		if knownAvailability {
			product.Available = &available
		}
	}
	return product, variants, nil
}

func crawlErrorFrom(stage, rawURL string, attempts int, err error) *CrawlError {
	result := &CrawlError{Stage: stage, URL: rawURL, Attempts: attempts, Message: err.Error()}
	var failure *httpFailure
	if errors.As(err, &failure) {
		result.Status = failure.Status
		if failure.Attempts > 0 {
			result.Attempts = failure.Attempts
		}
	}
	return result
}

func moneyMinor(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, errors.New("missing number")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return majorPriceToMinor(value)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, err
	}
	return strconv.ParseInt(number.String(), 10, 64)
}

func optionalMoneyMinor(raw json.RawMessage) (*int64, error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return nil, nil
	}
	value, err := moneyMinor(raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func majorPriceToMinor(value string) (int64, error) {
	value = strings.TrimSpace(value)
	amount, ok := new(big.Rat).SetString(value)
	if !ok {
		return 0, errors.New("invalid decimal money")
	}
	amount.Mul(amount, big.NewRat(100, 1))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(amount.Num(), amount.Denom(), remainder)
	if new(big.Int).Mul(new(big.Int).Abs(remainder), big.NewInt(2)).Cmp(new(big.Int).Abs(amount.Denom())) >= 0 {
		if amount.Sign() < 0 {
			quotient.Sub(quotient, big.NewInt(1))
		} else {
			quotient.Add(quotient, big.NewInt(1))
		}
	}
	if !quotient.IsInt64() {
		return 0, errors.New("money value exceeds int64")
	}
	return quotient.Int64(), nil
}

func normalizeProductImages(rawImages []json.RawMessage, rawFeatured, rawPrimary json.RawMessage) []ProductImageSnapshot {
	featured, hasFeatured := parseProductImage(rawFeatured)
	if !hasFeatured {
		featured, hasFeatured = parseProductImage(rawPrimary)
	}
	result := make([]ProductImageSnapshot, 0, len(rawImages)+1)
	seen := map[string]int{}
	for index, raw := range rawImages {
		image, ok := parseProductImage(raw)
		if !ok {
			continue
		}
		if image.Position <= 0 {
			image.Position = index + 1
		}
		if hasFeatured && image.URL == featured.URL {
			image.Featured = true
			if image.Alt == "" {
				image.Alt = featured.Alt
			}
		}
		if existing, duplicate := seen[image.URL]; duplicate {
			result[existing] = mergeProductImage(result[existing], image)
			continue
		}
		seen[image.URL] = len(result)
		result = append(result, image)
	}
	if hasFeatured {
		if existing, ok := seen[featured.URL]; ok {
			result[existing] = mergeProductImage(result[existing], featured)
			result[existing].Featured = true
		} else {
			featured.Featured = true
			featured.Position = 1
			result = append([]ProductImageSnapshot{featured}, result...)
		}
	}
	if len(result) > 0 {
		hasMarkedFeatured := false
		for _, image := range result {
			hasMarkedFeatured = hasMarkedFeatured || image.Featured
		}
		if !hasMarkedFeatured {
			result[0].Featured = true
		}
		for index := range result {
			if result[index].Position <= 0 {
				result[index].Position = index + 1
			}
		}
	}
	return result
}

func parseProductImage(raw json.RawMessage) (ProductImageSnapshot, bool) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return ProductImageSnapshot{}, false
	}
	var direct string
	if json.Unmarshal(raw, &direct) == nil {
		direct = absoluteAssetURL(direct)
		return ProductImageSnapshot{URL: direct}, direct != ""
	}
	var payload struct {
		Src        string            `json:"src"`
		URL        string            `json:"url"`
		Alt        string            `json:"alt"`
		Width      int               `json:"width"`
		Height     int               `json:"height"`
		Position   int               `json:"position"`
		VariantIDs []json.RawMessage `json:"variant_ids"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return ProductImageSnapshot{}, false
	}
	imageURL := payload.Src
	if imageURL == "" {
		imageURL = payload.URL
	}
	imageURL = absoluteAssetURL(imageURL)
	if imageURL == "" {
		return ProductImageSnapshot{}, false
	}
	variantIDs := make([]string, 0, len(payload.VariantIDs))
	for _, rawID := range payload.VariantIDs {
		if value := idString(rawID); value != "" {
			variantIDs = append(variantIDs, value)
		}
	}
	return ProductImageSnapshot{
		Position: payload.Position, URL: imageURL, Alt: strings.TrimSpace(payload.Alt), Width: payload.Width, Height: payload.Height,
		VariantIDs: uniqueSorted(variantIDs),
	}, true
}

func mergeProductImage(current, candidate ProductImageSnapshot) ProductImageSnapshot {
	if current.Alt == "" {
		current.Alt = candidate.Alt
	}
	if current.Width == 0 {
		current.Width = candidate.Width
	}
	if current.Height == 0 {
		current.Height = candidate.Height
	}
	if current.Position == 0 {
		current.Position = candidate.Position
	}
	current.VariantIDs = uniqueSorted(append(current.VariantIDs, candidate.VariantIDs...))
	current.Featured = current.Featured || candidate.Featured
	return current
}

func parsedImageURL(raw json.RawMessage) string {
	image, ok := parseProductImage(raw)
	if !ok {
		return ""
	}
	return image.URL
}

func featuredProductImage(images []ProductImageSnapshot) string {
	for _, image := range images {
		if image.Featured {
			return image.URL
		}
	}
	if len(images) > 0 {
		return images[0].URL
	}
	return ""
}

func imageForVariant(images []ProductImageSnapshot, variantID string) string {
	if variantID == "" {
		return ""
	}
	for _, image := range images {
		for _, candidate := range image.VariantIDs {
			if candidate == variantID {
				return image.URL
			}
		}
	}
	return ""
}

func productOptionNames(rawOptions []json.RawMessage) []string {
	result := make([]string, 0, len(rawOptions))
	seen := map[string]bool{}
	for _, raw := range rawOptions {
		name := ""
		var direct string
		if json.Unmarshal(raw, &direct) == nil {
			name = direct
		} else {
			var option struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(raw, &option) == nil {
				name = option.Name
			}
		}
		name = strings.TrimSpace(name)
		key := foldText(name)
		if key != "" && !seen[key] {
			seen[key] = true
			result = append(result, name)
		}
	}
	return result
}

func idString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return strings.Trim(string(raw), `"`)
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func absoluteAssetURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	return raw
}
