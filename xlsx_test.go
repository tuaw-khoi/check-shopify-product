package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"
)

func TestBuildWorkbookContainsFiveValidSheetsProductDetailsAndNumericPrices(t *testing.T) {
	available := true
	compareAt := int64(12000000)
	discountPct := 0.166666
	now := time.Date(2026, 9, 23, 10, 30, 0, 0, time.FixedZone("ICT", 7*3600))
	snapshot := ScanSnapshot{
		Status:   "completed",
		Store:    StoreMetadata{StoreURL: "https://example.com/", Name: "Demo", Currency: "VND"},
		Progress: ScanProgress{StartedAt: now, FinishedAt: &now},
		Summary:  ScanSummary{Products: 1, Images: 1, Variants: 1, Available: 1, Discounted: 1, MinPriceMinor: 10000000, MedianPriceMinor: 10000000, AveragePriceMinor: 10000000, MaxPriceMinor: 10000000},
		Products: []ProductSnapshot{{ProductID: "1", Title: "Sản phẩm & thử", Handle: "san-pham", DescriptionHTML: "<p>Mô tả & thử</p>", DescriptionText: "Mô tả & thử", Options: []string{"Size"}, ImageURL: "https://cdn.example.com/a.jpg", Images: []ProductImageSnapshot{{Position: 1, URL: "https://cdn.example.com/a.jpg", Alt: "Mặt trước", Width: 1000, Height: 1000, Featured: true}}, ImageCount: 1, VariantCount: 1, AvailableCount: 1, MinPriceMinor: 10000000, MaxPriceMinor: 10000000, URL: "https://example.com/products/san-pham"}},
		Variants: []VariantSnapshot{{ProductID: "1", VariantID: "2", ProductTitle: "Sản phẩm & thử", VariantTitle: "Default", PriceMinor: 10000000, CompareAtMinor: &compareAt, DiscountMinor: 2000000, DiscountPct: &discountPct, Available: &available, ProductURL: "https://example.com/products/san-pham", ScannedAt: now}},
	}
	content, filename, err := BuildWorkbook(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if filename != "shopify-price-snapshot_example.com_20260923_103000.xlsx" {
		t.Fatalf("filename=%q", filename)
	}
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, file := range reader.File {
		stream, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		body, readErr := io.ReadAll(stream)
		stream.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		files[file.Name] = body
		if strings.HasSuffix(file.Name, ".xml") {
			decoder := xml.NewDecoder(bytes.NewReader(body))
			for {
				if _, decodeErr := decoder.Token(); decodeErr == io.EOF {
					break
				} else if decodeErr != nil {
					t.Fatalf("invalid XML %s: %v", file.Name, decodeErr)
				}
			}
		}
	}
	for _, expected := range []string{"xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml", "xl/worksheets/sheet3.xml", "xl/worksheets/sheet4.xml", "xl/worksheets/sheet5.xml", "xl/styles.xml"} {
		if files[expected] == nil {
			t.Fatalf("missing %s", expected)
		}
	}
	productsXML := string(files["xl/worksheets/sheet2.xml"])
	if !strings.Contains(productsXML, `Mô tả &amp; thử`) || !strings.Contains(productsXML, `&lt;p&gt;Mô tả &amp; thử&lt;/p&gt;`) {
		t.Fatalf("expected plain and HTML descriptions in products sheet: %s", productsXML)
	}
	imagesXML := string(files["xl/worksheets/sheet3.xml"])
	if !strings.Contains(imagesXML, `Mặt trước`) || !strings.Contains(imagesXML, `<v>1000</v>`) {
		t.Fatalf("expected image metadata in images sheet: %s", imagesXML)
	}
	variantsXML := string(files["xl/worksheets/sheet4.xml"])
	if !strings.Contains(variantsXML, `<v>100000</v>`) {
		t.Fatalf("expected numeric VND major-unit price in variants sheet: %s", variantsXML)
	}
	if !strings.Contains(variantsXML, `Sản phẩm &amp; thử`) {
		t.Fatal("expected safely escaped Vietnamese product title")
	}
}

func TestSummaryMedian(t *testing.T) {
	available, unavailable := true, false
	variants := []VariantSnapshot{{PriceMinor: 100, Available: &available}, {PriceMinor: 300, Available: &unavailable}, {PriceMinor: 500, Available: nil, DiscountMinor: 20}, {PriceMinor: 700, Available: &available}}
	summary := summarize([]ProductSnapshot{{}, {}}, variants, []CrawlError{{Warning: true}, {Warning: false}})
	if summary.MedianPriceMinor != 400 || summary.AveragePriceMinor != 400 || summary.Available != 2 || summary.Unavailable != 1 || summary.UnknownAvailability != 1 || summary.Discounted != 1 || summary.Errors != 1 || summary.Warnings != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}
