package main

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

func TestBuildShopifyCSVMatchesCurrentImportColumns(t *testing.T) {
	compareAt := int64(2699000000)
	now := time.Date(2026, 10, 12, 9, 30, 0, 0, time.FixedZone("ICT", 7*3600))
	snapshot := ScanSnapshot{
		Status:   "completed",
		Store:    StoreMetadata{StoreURL: "https://www.example.com/", Currency: "VND"},
		Progress: ScanProgress{StartedAt: now},
		Products: []ProductSnapshot{{
			ProductID: "101", Title: "Điện thoại, bản mới", Handle: "dien-thoai-ban-moi",
			DescriptionHTML: "<p>Mô tả \"đầy đủ\"</p>", Vendor: "Demo", ProductType: "Phone",
			Tags: []string{"Điện thoại", "Mới"}, Options: []string{"Dung lượng", "Màu"},
			Images: []ProductImageSnapshot{
				{Position: 1, URL: "https://cdn.example.com/front.jpg", Alt: "Mặt trước"},
				{Position: 2, URL: "https://cdn.example.com/back.jpg", Alt: "Mặt sau"},
				{Position: 3, URL: "https://cdn.example.com/box.jpg", Alt: "Hộp"},
			},
		}},
		Variants: []VariantSnapshot{
			{ProductID: "101", VariantTitle: "128 GB / Đen", Option1: "128 GB", Option2: "Đen", SKU: "IP-128-B", Barcode: "123", PriceMinor: 2499000000, CompareAtMinor: &compareAt, ImageURL: "https://cdn.example.com/black.jpg"},
			{ProductID: "101", VariantTitle: "256 GB / Trắng", Option1: "256 GB", Option2: "Trắng", SKU: "IP-256-W", PriceMinor: 2799000000, ImageURL: "https://cdn.example.com/white.jpg"},
		},
	}

	content, filename, err := BuildShopifyCSV(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if filename != "shopify-import_example.com_20261012_093000.csv" {
		t.Fatalf("filename=%q", filename)
	}
	if bytes.Contains(content, []byte("\r\n")) {
		t.Fatal("Shopify CSV must use LF line endings")
	}

	records, err := csv.NewReader(bytes.NewReader(content)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("rows=%d, want header + 2 variants + 1 extra image", len(records))
	}
	columns := csvColumnIndexes(records[0])
	for _, required := range []string{"Title", "URL handle", "Description", "Option1 name", "Option1 value", "Price", "Product image URL", "Variant image URL"} {
		if _, ok := columns[required]; !ok {
			t.Fatalf("missing Shopify CSV column %q", required)
		}
	}

	first := records[1]
	if first[columns["Title"]] != "Điện thoại, bản mới" || first[columns["URL handle"]] != "dien-thoai-ban-moi" {
		t.Fatalf("unexpected product fields: %#v", first)
	}
	if first[columns["Description"]] != "<p>Mô tả \"đầy đủ\"</p>" || first[columns["Tags"]] != "Điện thoại, Mới" {
		t.Fatalf("CSV quoting damaged product text: %#v", first)
	}
	if first[columns["Option1 name"]] != "Dung lượng" || first[columns["Option1 value"]] != "128 GB" || first[columns["Option2 name"]] != "Màu" || first[columns["Option2 value"]] != "Đen" {
		t.Fatalf("variant options not mapped: %#v", first)
	}
	if first[columns["Price"]] != "24990000.00" || first[columns["Compare-at price"]] != "26990000.00" {
		t.Fatalf("prices not converted from minor units: %#v", first)
	}
	if first[columns["Product image URL"]] != "https://cdn.example.com/front.jpg" || first[columns["Variant image URL"]] != "https://cdn.example.com/black.jpg" {
		t.Fatalf("images not mapped: %#v", first)
	}

	second := records[2]
	if second[columns["Title"]] != "" || second[columns["URL handle"]] != "dien-thoai-ban-moi" || second[columns["SKU"]] != "IP-256-W" {
		t.Fatalf("subsequent variant row invalid: %#v", second)
	}
	extraImage := records[3]
	if extraImage[columns["URL handle"]] != "dien-thoai-ban-moi" || extraImage[columns["Product image URL"]] != "https://cdn.example.com/box.jpg" || extraImage[columns["Image position"]] != "3" {
		t.Fatalf("extra image row invalid: %#v", extraImage)
	}
}

func TestBuildShopifyCSVUsesDefaultVariantWhenOptionsAreMissing(t *testing.T) {
	snapshot := ScanSnapshot{
		Products: []ProductSnapshot{{ProductID: "1", Title: "Simple", Handle: "simple", MinPriceMinor: 1250}},
		Variants: []VariantSnapshot{{ProductID: "1", PriceMinor: 1250}},
	}
	content, _, err := BuildShopifyCSV(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(string(content))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	columns := csvColumnIndexes(records[0])
	if records[1][columns["Option1 name"]] != "Default Title" || records[1][columns["Option1 value"]] != "Default Title" || records[1][columns["Price"]] != "12.50" {
		t.Fatalf("default variant row invalid: %#v", records[1])
	}
}

func csvColumnIndexes(headers []string) map[string]int {
	result := make(map[string]int, len(headers))
	for index, header := range headers {
		result[header] = index
	}
	return result
}
