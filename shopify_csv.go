package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const shopifyCSVMaxBytes = 15 * 1024 * 1024

// Keep this focused on fields the public storefront exposes reliably. Omitting
// unknown optional columns is safer than importing blank values over store data.
var shopifyCSVHeaders = []string{
	"Title",
	"URL handle",
	"Description",
	"Vendor",
	"Type",
	"Tags",
	"Published on online store",
	"Status",
	"SKU",
	"Variant Barcodes",
	"Option1 name",
	"Option1 value",
	"Option2 name",
	"Option2 value",
	"Option3 name",
	"Option3 value",
	"Price",
	"Compare-at price",
	"Product image URL",
	"Image position",
	"Image alt text",
	"Variant image URL",
}

func BuildShopifyCSV(snapshot ScanSnapshot) ([]byte, string, error) {
	if len(snapshot.Products) == 0 {
		return nil, "", errors.New("snapshot không có product để xuất Shopify CSV")
	}

	variantsByProduct := make(map[string][]VariantSnapshot, len(snapshot.Products))
	for _, variant := range snapshot.Variants {
		variantsByProduct[variant.ProductID] = append(variantsByProduct[variant.ProductID], variant)
	}

	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	writer.UseCRLF = false
	if err := writer.Write(shopifyCSVHeaders); err != nil {
		return nil, "", fmt.Errorf("không ghi được header Shopify CSV: %w", err)
	}

	for _, product := range snapshot.Products {
		variants := variantsByProduct[product.ProductID]
		images := shopifyProductImages(product)
		if len(variants) == 0 {
			variants = []VariantSnapshot{{
				ProductID: product.ProductID, ProductTitle: product.Title,
				VariantTitle: "Default Title", PriceMinor: product.MinPriceMinor,
			}}
		}

		for index, variant := range variants {
			row := newShopifyCSVRow()
			row["URL handle"] = product.Handle
			if index == 0 {
				row["Title"] = product.Title
				row["Description"] = product.DescriptionHTML
				if row["Description"] == "" {
					row["Description"] = product.DescriptionText
				}
				row["Vendor"] = product.Vendor
				row["Type"] = product.ProductType
				row["Tags"] = strings.Join(product.Tags, ", ")
				row["Published on online store"] = "TRUE"
				row["Status"] = "Active"
			}
			fillShopifyVariant(row, product, variant)
			if index < len(images) {
				fillShopifyImage(row, images[index], index+1)
			}
			if err := writer.Write(shopifyCSVRecord(row)); err != nil {
				return nil, "", fmt.Errorf("không ghi được variant Shopify CSV: %w", err)
			}
		}

		for index := len(variants); index < len(images); index++ {
			row := newShopifyCSVRow()
			row["URL handle"] = product.Handle
			fillShopifyImage(row, images[index], index+1)
			if err := writer.Write(shopifyCSVRecord(row)); err != nil {
				return nil, "", fmt.Errorf("không ghi được image Shopify CSV: %w", err)
			}
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, "", fmt.Errorf("không hoàn tất được Shopify CSV: %w", err)
	}
	if output.Len() > shopifyCSVMaxBytes {
		return nil, "", fmt.Errorf("Shopify CSV có dung lượng %.1f MB, vượt giới hạn import 15 MB; hãy quét theo collection hoặc cụm tên nhỏ hơn", float64(output.Len())/(1024*1024))
	}

	createdAt := snapshot.Progress.StartedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	host := "store"
	if parsed, err := url.Parse(snapshot.Store.StoreURL); err == nil && parsed.Hostname() != "" {
		host = strings.TrimPrefix(parsed.Hostname(), "www.")
	}
	filename := "shopify-import_" + safeFilename(host) + "_" + createdAt.Format("20060102_150405") + ".csv"
	return output.Bytes(), filename, nil
}

func newShopifyCSVRow() map[string]string {
	return make(map[string]string, len(shopifyCSVHeaders))
}

func shopifyCSVRecord(row map[string]string) []string {
	record := make([]string, len(shopifyCSVHeaders))
	for index, header := range shopifyCSVHeaders {
		record[index] = row[header]
	}
	return record
}

func fillShopifyVariant(row map[string]string, product ProductSnapshot, variant VariantSnapshot) {
	row["SKU"] = variant.SKU
	row["Variant Barcodes"] = variant.Barcode

	optionNames := append([]string(nil), product.Options...)
	optionValues := []string{variant.Option1, variant.Option2, variant.Option3}
	if len(optionNames) == 0 || strings.TrimSpace(optionNames[0]) == "" {
		optionNames = []string{"Default Title"}
		optionValues[0] = "Default Title"
	}
	for index := 0; index < 3 && index < len(optionNames); index++ {
		name := strings.TrimSpace(optionNames[index])
		if name == "" {
			continue
		}
		value := strings.TrimSpace(optionValues[index])
		if index == 0 && name == "Default Title" {
			value = "Default Title"
		}
		row[fmt.Sprintf("Option%d name", index+1)] = name
		row[fmt.Sprintf("Option%d value", index+1)] = value
	}

	row["Price"] = shopifyPrice(variant.PriceMinor)
	if variant.CompareAtMinor != nil && *variant.CompareAtMinor > 0 {
		row["Compare-at price"] = shopifyPrice(*variant.CompareAtMinor)
	}
	row["Variant image URL"] = variant.ImageURL
}

func fillShopifyImage(row map[string]string, image ProductImageSnapshot, fallbackPosition int) {
	position := image.Position
	if position <= 0 {
		position = fallbackPosition
	}
	row["Product image URL"] = image.URL
	row["Image position"] = strconv.Itoa(position)
	row["Image alt text"] = image.Alt
}

func shopifyProductImages(product ProductSnapshot) []ProductImageSnapshot {
	images := append([]ProductImageSnapshot(nil), product.Images...)
	if len(images) == 0 && product.ImageURL != "" {
		images = append(images, ProductImageSnapshot{Position: 1, URL: product.ImageURL, Featured: true})
	}
	return images
}

func shopifyPrice(minor int64) string {
	return strconv.FormatFloat(float64(minor)/100, 'f', 2, 64)
}
