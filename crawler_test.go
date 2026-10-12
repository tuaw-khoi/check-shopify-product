package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newFakeShopify(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var retryCounter atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><head><title>Cửa hàng thử nghiệm</title></head><body class="shopify-section"><script>Shopify.routes.root = '/'; Shopify.theme = {};</script><script id="captcha-bootstrap">window.ShopifyCaptcha='hcaptcha';</script></body></html>`)
		case "/cart.js":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"currency":"VND"}`)
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0"?><sitemapindex><sitemap><loc>%s/sitemap_products.xml</loc></sitemap><sitemap><loc>%s/sitemap_collections.xml</loc></sitemap></sitemapindex>`, base, base)
		case "/sitemap_products.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0"?><urlset><url><loc>%s/products/iphone-16</loc></url><url><loc>%s/products/macbook-air</loc></url><url><loc>%s/products/broken</loc></url></urlset>`, base, base, base)
		case "/sitemap_collections.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0"?><urlset><url><loc>%s/collections/apple</loc></url></urlset>`, base)
		case "/collections/apple/products.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"products":[{"handle":"iphone-16"},{"handle":"macbook-air"}]}`)
		case "/products/iphone-16.js":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":101,"title":"Điện thoại iPhone 16","handle":"iphone-16","description":"<p>Điện thoại <strong>mới</strong>.</p>","published_at":"2026-09-20T08:00:00+07:00","created_at":"2026-09-19T08:00:00+07:00","vendor":"Apple","type":"Phone","tags":["Điện thoại","Mới"],"featured_image":"//cdn.shopify.com/iphone.jpg","images":["//cdn.shopify.com/iphone.jpg","//cdn.shopify.com/iphone-back.jpg"],"options":[{"name":"Dung lượng","position":1},{"name":"Màu","position":2}],"variants":[{"id":1001,"title":"128 GB / Đen","option1":"128 GB","option2":"Đen","option3":null,"sku":"IP16-128-B","barcode":"123","price":2499000000,"compare_at_price":2699000000,"available":true,"featured_image":{"src":"//cdn.shopify.com/iphone-black.jpg","width":1200,"height":1200}},{"id":1002,"title":"256 GB / Trắng","option1":"256 GB","option2":"Trắng","price":2799000000,"compare_at_price":null,"available":false}]}`)
		case "/products/macbook-air.js":
			if retryCounter.Add(1) == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprint(w, "slow down")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":102,"title":"MacBook Air","handle":"macbook-air","vendor":"Apple","type":"Laptop","tags":[],"variants":[{"id":2001,"title":"Default","price":3000000000,"compare_at_price":null,"available":true}]}`)
		case "/products/broken.js":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{broken`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &retryCounter
}

func TestDiscoverAndScanCollectionWithTitleFilter(t *testing.T) {
	server, retryCounter := newFakeShopify(t)
	guard := URLGuard{AllowPrivate: true}
	crawler, discovery, err := DiscoverStore(context.Background(), server.URL, guard)
	if err != nil {
		t.Fatal(err)
	}
	if discovery.Metadata.Currency != "VND" || discovery.Metadata.ProductCount != 3 || len(discovery.Metadata.Collections) != 1 {
		t.Fatalf("unexpected discovery: %#v", discovery.Metadata)
	}

	var products []ProductSnapshot
	var variants []VariantSnapshot
	var crawlErrors []CrawlError
	progressRead := 0
	err = crawler.Scan(context.Background(), discovery, ScanRequest{StoreURL: server.URL, Collections: []string{"apple"}, TitlePhrases: []string{"dien thoai iphone 16"}}, scanHooks{
		SetProgress: func(_, read, _ int, _ string) { progressRead = read },
		AddProduct: func(product ProductSnapshot, entries []VariantSnapshot) {
			products = append(products, product)
			variants = append(variants, entries...)
		},
		AddError: func(crawlErr CrawlError) { crawlErrors = append(crawlErrors, crawlErr) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 1 || products[0].Handle != "iphone-16" || len(variants) != 2 {
		t.Fatalf("unexpected filtered result: products=%#v variants=%d", products, len(variants))
	}
	if progressRead != 2 {
		t.Fatalf("read %d products, want 2", progressRead)
	}
	if variants[0].DiscountMinor != 200000000 || variants[0].DiscountPct == nil {
		t.Fatalf("discount not calculated: %#v", variants[0])
	}
	if products[0].DescriptionText != "Điện thoại mới." || products[0].ImageCount != 2 || len(products[0].Options) != 2 {
		t.Fatalf("product detail not normalized: %#v", products[0])
	}
	if variants[0].ImageURL != "https://cdn.shopify.com/iphone-black.jpg" {
		t.Fatalf("variant image not normalized: %#v", variants[0])
	}
	if retryCounter.Load() != 2 {
		t.Fatalf("expected one retry for macbook, requests=%d", retryCounter.Load())
	}
	if len(crawlErrors) != 0 {
		t.Fatalf("unexpected errors: %#v", crawlErrors)
	}
}

func TestScanContinuesAfterMalformedProduct(t *testing.T) {
	server, _ := newFakeShopify(t)
	crawler, discovery, err := DiscoverStore(context.Background(), server.URL, URLGuard{AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	var products int
	var errorsSeen []CrawlError
	err = crawler.Scan(context.Background(), discovery, ScanRequest{StoreURL: server.URL}, scanHooks{
		SetProgress: func(_, _, _ int, _ string) {},
		AddProduct:  func(_ ProductSnapshot, _ []VariantSnapshot) { products++ },
		AddError:    func(crawlErr CrawlError) { errorsSeen = append(errorsSeen, crawlErr) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if products != 2 {
		t.Fatalf("products=%d, want 2 successful products", products)
	}
	foundMalformed := false
	for _, crawlErr := range errorsSeen {
		if strings.Contains(crawlErr.Message, "JSON product") {
			foundMalformed = true
		}
	}
	if !foundMalformed {
		t.Fatalf("missing malformed JSON error: %#v", errorsSeen)
	}
}

func TestFetchHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()
	guard := URLGuard{AllowPrivate: true}
	client, _ := guard.NewHTTPClient(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	u, _ := guard.NormalizeStoreURL(server.URL)
	_, _, _, err := fetchWithRetry(ctx, client, guard, u, nil, "text/plain", 1024)
	if err == nil || !strings.Contains(err.Error(), "context deadline") {
		t.Fatalf("expected context deadline, got %v", err)
	}
}

func TestURLGuardBlocksPrivateNetworks(t *testing.T) {
	if _, err := (URLGuard{}).NormalizeStoreURL("http://127.0.0.1"); err == nil {
		t.Fatal("expected private network rejection")
	}
	if _, err := (URLGuard{AllowPrivate: true}).NormalizeStoreURL("http://127.0.0.1:1234"); err != nil {
		t.Fatalf("private URL should be allowed in tests: %v", err)
	}
}

func TestBulkCatalogAvoidsCollectionAndPerProductRequests(t *testing.T) {
	var collectionRequests atomic.Int32
	var productRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><head><title>Bulk Store</title></head><body class="shopify-section"><script>Shopify.routes.root='/'; Shopify.theme={};</script></body></html>`)
		case "/cart.js":
			fmt.Fprint(w, `{"currency":"USD"}`)
		case "/sitemap.xml":
			fmt.Fprintf(w, `<?xml version="1.0"?><urlset><url><loc>%s/products/one</loc></url><url><loc>%s/products/two</loc></url><url><loc>%s/products/three</loc></url><url><loc>%s/collections/demo</loc></url></urlset>`, base, base, base, base)
		case "/products.json":
			fmt.Fprint(w, `{"products":[`+
				`{"id":1,"title":"One","handle":"one","body_html":"<p>Bulk description</p>","vendor":"V","product_type":"Bag","tags":"A, B","image":{"src":"//cdn.shopify.com/one.jpg"},"images":[{"src":"//cdn.shopify.com/one.jpg","alt":"Front","width":900,"height":900,"position":1,"variant_ids":[11]}],"options":[{"name":"Title","position":1}],"variants":[{"id":11,"title":"Default","price":"24.00","compare_at_price":"30.00","available":true}]},`+
				`{"id":2,"title":"Two","handle":"two","vendor":"V","product_type":"Bag","tags":"A","variants":[{"id":21,"title":"Default","price":"25.50","compare_at_price":"","available":false}]},`+
				`{"id":3,"title":"Three","handle":"three","vendor":"V","product_type":"Wallet","tags":"B","variants":[{"id":31,"title":"Default","price":"26.00","compare_at_price":null,"available":true}]}`+
				`]}`)
		case "/collections/demo/products.json", "/collections/demo":
			collectionRequests.Add(1)
			http.Error(w, "should not be called", http.StatusTooManyRequests)
		default:
			if strings.HasPrefix(r.URL.Path, "/products/") {
				productRequests.Add(1)
			}
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	crawler, discovery, err := DiscoverStore(context.Background(), server.URL, URLGuard{AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	var products []ProductSnapshot
	var variants []VariantSnapshot
	err = crawler.Scan(context.Background(), discovery, ScanRequest{StoreURL: server.URL}, scanHooks{
		SetProgress: func(_, _, _ int, _ string) {},
		AddProduct: func(product ProductSnapshot, entries []VariantSnapshot) {
			products = append(products, product)
			variants = append(variants, entries...)
		},
		AddError: func(CrawlError) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 3 || len(variants) != 3 {
		t.Fatalf("bulk result products=%d variants=%d", len(products), len(variants))
	}
	if collectionRequests.Load() != 0 || productRequests.Load() != 0 {
		t.Fatalf("unexpected fallback calls: collections=%d products=%d", collectionRequests.Load(), productRequests.Load())
	}
	if variants[0].PriceMinor != 2400 || variants[0].DiscountMinor != 600 {
		t.Fatalf("bulk decimal price conversion failed: %#v", variants[0])
	}
	if products[0].ProductType != "Bag" || products[0].ImageURL != "https://cdn.shopify.com/one.jpg" {
		t.Fatalf("bulk product normalization failed: %#v", products[0])
	}
	if products[0].DescriptionText != "Bulk description" || products[0].ImageCount != 1 || products[0].Images[0].Alt != "Front" || variants[0].ImageURL != "https://cdn.shopify.com/one.jpg" {
		t.Fatalf("bulk product detail normalization failed: product=%#v variant=%#v", products[0], variants[0])
	}
}

func TestMoneyMinorSupportsAjaxIntegersAndBulkDecimals(t *testing.T) {
	tests := []struct {
		raw  string
		want int64
	}{
		{`2499`, 2499},
		{`"24.00"`, 2400},
		{`"25.50"`, 2550},
		{`"0.009"`, 1},
	}
	for _, test := range tests {
		got, err := moneyMinor(json.RawMessage(test.raw))
		if err != nil || got != test.want {
			t.Fatalf("moneyMinor(%s)=%d,%v want %d", test.raw, got, err, test.want)
		}
	}
}

func TestBotChallengeDetectionIgnoresNormalShopifyCaptchaBootstrap(t *testing.T) {
	storeURL, _ := url.Parse("https://example.com/")
	normalHomepage := []byte(`<html><head><title>My Store</title></head><body><script id="captcha-bootstrap">const provider='hcaptcha'; const challenge='form challenge';</script></body></html>`)
	if isActiveBotChallenge(storeURL, normalHomepage) {
		t.Fatal("normal Shopify captcha bootstrap must not be treated as an active challenge")
	}

	challengeURL, _ := url.Parse("https://example.com/challenge")
	if !isActiveBotChallenge(challengeURL, normalHomepage) {
		t.Fatal("challenge route should be detected")
	}
	verificationPage := []byte(`<html><head><title>Verify you are human</title></head><body><form id="challenge-form"><div class="hcaptcha"></div></form></body></html>`)
	if !isActiveBotChallenge(storeURL, verificationPage) {
		t.Fatal("visible verification page should be detected")
	}
}
