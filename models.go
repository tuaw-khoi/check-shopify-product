package main

import "time"

const appVersion = "1.2.0"

type CollectionRef struct {
	Title  string `json:"title"`
	Handle string `json:"handle"`
	URL    string `json:"url"`
}

type StoreMetadata struct {
	StoreURL     string          `json:"store_url"`
	Name         string          `json:"name"`
	LocaleRoot   string          `json:"locale_root"`
	Currency     string          `json:"currency"`
	ProductCount int             `json:"product_count"`
	Collections  []CollectionRef `json:"collections"`
	Warnings     []string        `json:"warnings,omitempty"`
}

type ScanRequest struct {
	StoreURL     string   `json:"store_url"`
	Collections  []string `json:"collections"`
	TitlePhrases []string `json:"title_phrases"`
}

type ProductImageSnapshot struct {
	Position   int      `json:"position"`
	URL        string   `json:"url"`
	Alt        string   `json:"alt"`
	Width      int      `json:"width,omitempty"`
	Height     int      `json:"height,omitempty"`
	VariantIDs []string `json:"variant_ids,omitempty"`
	Featured   bool     `json:"featured"`
}

type ProductSnapshot struct {
	ProductID           string                 `json:"product_id"`
	Title               string                 `json:"title"`
	Handle              string                 `json:"handle"`
	DescriptionHTML     string                 `json:"description_html"`
	DescriptionText     string                 `json:"description_text"`
	Vendor              string                 `json:"vendor"`
	ProductType         string                 `json:"product_type"`
	Tags                []string               `json:"tags"`
	Collections         []string               `json:"collections"`
	Options             []string               `json:"options"`
	PublishedAt         string                 `json:"published_at"`
	CreatedAt           string                 `json:"created_at"`
	Available           *bool                  `json:"available"`
	URL                 string                 `json:"url"`
	ImageURL            string                 `json:"image_url"`
	Images              []ProductImageSnapshot `json:"images"`
	ImageCount          int                    `json:"image_count"`
	VariantCount        int                    `json:"variant_count"`
	AvailableCount      int                    `json:"available_count"`
	MinPriceMinor       int64                  `json:"min_price_minor"`
	MaxPriceMinor       int64                  `json:"max_price_minor"`
	VariantLimitWarning bool                   `json:"variant_limit_warning"`
}

type VariantSnapshot struct {
	ProductID      string    `json:"product_id"`
	VariantID      string    `json:"variant_id"`
	ProductTitle   string    `json:"product_title"`
	VariantTitle   string    `json:"variant_title"`
	Option1        string    `json:"option1"`
	Option2        string    `json:"option2"`
	Option3        string    `json:"option3"`
	SKU            string    `json:"sku"`
	Barcode        string    `json:"barcode"`
	Vendor         string    `json:"vendor"`
	ProductType    string    `json:"product_type"`
	Tags           []string  `json:"tags"`
	Collections    []string  `json:"collections"`
	PriceMinor     int64     `json:"price_minor"`
	CompareAtMinor *int64    `json:"compare_at_minor"`
	DiscountMinor  int64     `json:"discount_minor"`
	DiscountPct    *float64  `json:"discount_pct"`
	Available      *bool     `json:"available"`
	ImageURL       string    `json:"image_url"`
	ProductURL     string    `json:"product_url"`
	ScannedAt      time.Time `json:"scanned_at"`
}

type CrawlError struct {
	Stage    string `json:"stage"`
	URL      string `json:"url"`
	Status   int    `json:"status,omitempty"`
	Attempts int    `json:"attempts"`
	Message  string `json:"message"`
	Warning  bool   `json:"warning"`
}

type ScanSummary struct {
	Products            int     `json:"products"`
	Images              int     `json:"images"`
	Variants            int     `json:"variants"`
	Available           int     `json:"available"`
	Unavailable         int     `json:"unavailable"`
	UnknownAvailability int     `json:"unknown_availability"`
	Discounted          int     `json:"discounted"`
	Errors              int     `json:"errors"`
	Warnings            int     `json:"warnings"`
	MinPriceMinor       int64   `json:"min_price_minor"`
	MedianPriceMinor    float64 `json:"median_price_minor"`
	AveragePriceMinor   float64 `json:"average_price_minor"`
	MaxPriceMinor       int64   `json:"max_price_minor"`
}

type ScanProgress struct {
	Discovered int        `json:"discovered"`
	Read       int        `json:"read"`
	Matched    int        `json:"matched"`
	Errors     int        `json:"errors"`
	Message    string     `json:"message"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type ScanSnapshot struct {
	ID       string            `json:"id"`
	Status   string            `json:"status"`
	Error    string            `json:"error,omitempty"`
	Store    StoreMetadata     `json:"store"`
	Request  ScanRequest       `json:"request"`
	Progress ScanProgress      `json:"progress"`
	Summary  ScanSummary       `json:"summary"`
	Products []ProductSnapshot `json:"products"`
	Variants []VariantSnapshot `json:"variants"`
	Errors   []CrawlError      `json:"errors"`
}
