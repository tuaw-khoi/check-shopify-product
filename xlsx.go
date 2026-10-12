package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type workbookCell struct {
	Value     any
	Style     int
	Hyperlink string
}

type workbookSheet struct {
	Name   string
	Rows   [][]workbookCell
	Widths []float64
}

func textCell(value any) workbookCell      { return workbookCell{Value: fmt.Sprint(value)} }
func headerCell(value string) workbookCell { return workbookCell{Value: value, Style: 1} }
func moneyCell(minor float64) workbookCell { return workbookCell{Value: minor / 100, Style: 2} }
func percentCell(value *float64) workbookCell {
	if value == nil {
		return workbookCell{}
	}
	return workbookCell{Value: *value, Style: 3}
}
func linkCell(label, target string) workbookCell {
	return workbookCell{Value: label, Style: 4, Hyperlink: target}
}

func BuildWorkbook(snapshot ScanSnapshot) ([]byte, string, error) {
	createdAt := time.Now()
	if !snapshot.Progress.StartedAt.IsZero() {
		createdAt = snapshot.Progress.StartedAt
	}
	scope := "Toàn bộ sản phẩm"
	if len(snapshot.Request.Collections) > 0 {
		scope = "Collections: " + strings.Join(snapshot.Request.Collections, ", ")
	}
	if len(snapshot.Request.TitlePhrases) > 0 {
		scope += "; Cụm tên: " + strings.Join(snapshot.Request.TitlePhrases, ", ")
	}
	finishedAt := ""
	if snapshot.Progress.FinishedAt != nil {
		finishedAt = snapshot.Progress.FinishedAt.Local().Format("2006-01-02 15:04:05")
	}
	summaryRows := [][]workbookCell{
		{headerCell("Thông số"), headerCell("Giá trị")},
		{textCell("Store"), linkCell(snapshot.Store.StoreURL, snapshot.Store.StoreURL)},
		{textCell("Tên store"), textCell(snapshot.Store.Name)},
		{textCell("Currency"), textCell(snapshot.Store.Currency)},
		{textCell("Phạm vi"), textCell(scope)},
		{textCell("Bắt đầu"), textCell(snapshot.Progress.StartedAt.Local().Format("2006-01-02 15:04:05"))},
		{textCell("Hoàn tất"), textCell(finishedAt)},
		{textCell("Trạng thái"), textCell(snapshot.Status)},
		{textCell("Products"), {Value: snapshot.Summary.Products}},
		{textCell("Images"), {Value: snapshot.Summary.Images}},
		{textCell("Variants"), {Value: snapshot.Summary.Variants}},
		{textCell("Còn hàng"), {Value: snapshot.Summary.Available}},
		{textCell("Hết hàng"), {Value: snapshot.Summary.Unavailable}},
		{textCell("Tồn kho không rõ"), {Value: snapshot.Summary.UnknownAvailability}},
		{textCell("Đang giảm giá"), {Value: snapshot.Summary.Discounted}},
		{textCell("Lỗi"), {Value: snapshot.Summary.Errors}},
		{textCell("Cảnh báo"), {Value: snapshot.Summary.Warnings}},
		{textCell("Giá thấp nhất"), moneyCell(float64(snapshot.Summary.MinPriceMinor))},
		{textCell("Giá trung vị"), moneyCell(snapshot.Summary.MedianPriceMinor)},
		{textCell("Giá trung bình"), moneyCell(snapshot.Summary.AveragePriceMinor)},
		{textCell("Giá cao nhất"), moneyCell(float64(snapshot.Summary.MaxPriceMinor))},
	}

	productHeaders := []string{"Product ID", "Tên product", "Handle", "Mô tả", "Mô tả HTML", "Vendor", "Product type", "Tags", "Collections", "Options", "Published at", "Created at", "Trạng thái", "Số variants", "Variants còn hàng", "Giá thấp nhất", "Giá cao nhất", "Số ảnh", "Cảnh báo 250 variants", "Product URL", "Ảnh đại diện"}
	productRows := [][]workbookCell{headerRow(productHeaders)}
	for _, product := range snapshot.Products {
		productRows = append(productRows, []workbookCell{
			textCell(product.ProductID), textCell(product.Title), textCell(product.Handle), textCell(excelText(product.DescriptionText)), textCell(excelText(product.DescriptionHTML)),
			textCell(product.Vendor), textCell(product.ProductType), textCell(strings.Join(product.Tags, ", ")), textCell(strings.Join(product.Collections, ", ")), textCell(strings.Join(product.Options, ", ")),
			textCell(product.PublishedAt), textCell(product.CreatedAt), textCell(availabilityText(product.Available)), {Value: product.VariantCount}, {Value: product.AvailableCount},
			moneyCell(float64(product.MinPriceMinor)), moneyCell(float64(product.MaxPriceMinor)), {Value: product.ImageCount}, textCell(yesNo(product.VariantLimitWarning)),
			linkCell(product.URL, product.URL), linkCell(product.ImageURL, product.ImageURL),
		})
	}

	imageHeaders := []string{"Product ID", "Tên product", "Vị trí", "Ảnh đại diện", "Alt text", "Width", "Height", "Variant IDs", "Image URL", "Product URL"}
	imageRows := [][]workbookCell{headerRow(imageHeaders)}
	for _, product := range snapshot.Products {
		for _, image := range product.Images {
			imageRows = append(imageRows, []workbookCell{
				textCell(product.ProductID), textCell(product.Title), {Value: image.Position}, textCell(yesNo(image.Featured)), textCell(image.Alt),
				{Value: image.Width}, {Value: image.Height}, textCell(strings.Join(image.VariantIDs, ", ")), linkCell(image.URL, image.URL), linkCell(product.URL, product.URL),
			})
		}
	}

	variantHeaders := []string{"Product ID", "Variant ID", "Tên product", "Tên variant", "Option 1", "Option 2", "Option 3", "SKU", "Barcode", "Vendor", "Product type", "Tags", "Collections", "Giá", "Compare-at price", "Giảm", "% giảm", "Tồn hàng", "Currency", "Product URL", "Ảnh", "Thời điểm quét"}
	variantRows := [][]workbookCell{headerRow(variantHeaders)}
	for _, variant := range snapshot.Variants {
		compareAt := workbookCell{}
		if variant.CompareAtMinor != nil {
			compareAt = moneyCell(float64(*variant.CompareAtMinor))
		}
		variantRows = append(variantRows, []workbookCell{
			textCell(variant.ProductID), textCell(variant.VariantID), textCell(variant.ProductTitle), textCell(variant.VariantTitle),
			textCell(variant.Option1), textCell(variant.Option2), textCell(variant.Option3), textCell(variant.SKU), textCell(variant.Barcode),
			textCell(variant.Vendor), textCell(variant.ProductType), textCell(strings.Join(variant.Tags, ", ")), textCell(strings.Join(variant.Collections, ", ")),
			moneyCell(float64(variant.PriceMinor)), compareAt, moneyCell(float64(variant.DiscountMinor)), percentCell(variant.DiscountPct),
			textCell(availabilityText(variant.Available)), textCell(snapshot.Store.Currency), linkCell(variant.ProductURL, variant.ProductURL), linkCell(variant.ImageURL, variant.ImageURL),
			textCell(variant.ScannedAt.Local().Format("2006-01-02 15:04:05")),
		})
	}

	errorHeaders := []string{"Loại", "URL", "HTTP status", "Số lần thử", "Mức độ", "Nội dung"}
	errorRows := [][]workbookCell{headerRow(errorHeaders)}
	for _, crawlErr := range snapshot.Errors {
		severity := "Lỗi"
		if crawlErr.Warning {
			severity = "Cảnh báo"
		}
		errorRows = append(errorRows, []workbookCell{
			textCell(crawlErr.Stage), linkCell(crawlErr.URL, crawlErr.URL), {Value: crawlErr.Status}, {Value: crawlErr.Attempts}, textCell(severity), textCell(crawlErr.Message),
		})
	}

	sheets := []workbookSheet{
		{Name: "Summary", Rows: summaryRows, Widths: []float64{24, 72}},
		{Name: "Products", Rows: productRows, Widths: []float64{18, 34, 24, 54, 54, 20, 20, 28, 30, 24, 22, 22, 16, 12, 16, 16, 16, 12, 18, 48, 48}},
		{Name: "Images", Rows: imageRows, Widths: []float64{18, 34, 10, 14, 34, 12, 12, 28, 58, 48}},
		{Name: "Variants", Rows: variantRows, Widths: []float64{18, 18, 34, 28, 18, 18, 18, 18, 18, 20, 20, 28, 30, 16, 18, 16, 12, 16, 12, 48, 48, 22}},
		{Name: "Errors", Rows: errorRows, Widths: []float64{18, 56, 14, 14, 14, 72}},
	}
	content, err := createXLSX(sheets, snapshot.Store.Currency, createdAt)
	if err != nil {
		return nil, "", fmt.Errorf("không tạo được Excel: %w", err)
	}
	host := "store"
	if parsed, err := url.Parse(snapshot.Store.StoreURL); err == nil && parsed.Hostname() != "" {
		host = strings.TrimPrefix(parsed.Hostname(), "www.")
	}
	filename := "shopify-price-snapshot_" + safeFilename(host) + "_" + createdAt.Format("20060102_150405") + ".xlsx"
	return content, filename, nil
}

func excelText(value string) string {
	const maxCellCharacters = 32700
	value = strings.TrimSpace(value)
	if len([]rune(value)) <= maxCellCharacters {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxCellCharacters-1]) + "…"
}

func headerRow(values []string) []workbookCell {
	row := make([]workbookCell, len(values))
	for index, value := range values {
		row[index] = headerCell(value)
	}
	return row
}

func yesNo(value bool) string {
	if value {
		return "Có"
	}
	return "Không"
}

func availabilityText(value *bool) string {
	if value == nil {
		return "Không rõ"
	}
	if *value {
		return "Còn hàng"
	}
	return "Hết hàng"
}

var unsafeFilename = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeFilename(value string) string {
	value = strings.Trim(unsafeFilename.ReplaceAllString(value, "-"), "-.")
	if value == "" {
		return "store"
	}
	return value
}

func createXLSX(sheets []workbookSheet, currency string, createdAt time.Time) ([]byte, error) {
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	writePart := func(name, content string) error {
		part, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = part.Write([]byte(content))
		return err
	}

	if err := writePart("[Content_Types].xml", contentTypesXML(len(sheets))); err != nil {
		return nil, err
	}
	if err := writePart("_rels/.rels", rootRelationshipsXML()); err != nil {
		return nil, err
	}
	if err := writePart("docProps/core.xml", corePropertiesXML(createdAt)); err != nil {
		return nil, err
	}
	if err := writePart("docProps/app.xml", appPropertiesXML(sheets)); err != nil {
		return nil, err
	}
	if err := writePart("xl/workbook.xml", workbookXML(sheets)); err != nil {
		return nil, err
	}
	if err := writePart("xl/_rels/workbook.xml.rels", workbookRelationshipsXML(len(sheets))); err != nil {
		return nil, err
	}
	if err := writePart("xl/styles.xml", stylesXML(currency)); err != nil {
		return nil, err
	}
	for index, sheet := range sheets {
		sheetXML, relationships := worksheetXML(sheet)
		if err := writePart(fmt.Sprintf("xl/worksheets/sheet%d.xml", index+1), sheetXML); err != nil {
			return nil, err
		}
		if relationships != "" {
			if err := writePart(fmt.Sprintf("xl/worksheets/_rels/sheet%d.xml.rels", index+1), relationships); err != nil {
				return nil, err
			}
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func contentTypesXML(sheetCount int) string {
	var overrides strings.Builder
	for index := 1; index <= sheetCount; index++ {
		fmt.Fprintf(&overrides, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, index)
	}
	return xmlHeader + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/><Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>` + overrides.String() + `</Types>`
}

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`

func rootRelationshipsXML() string {
	return xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/></Relationships>`
}

func corePropertiesXML(createdAt time.Time) string {
	timestamp := createdAt.UTC().Format(time.RFC3339)
	return xmlHeader + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:creator>Shopify Price Checker</dc:creator><cp:lastModifiedBy>Shopify Price Checker</cp:lastModifiedBy><dcterms:created xsi:type="dcterms:W3CDTF">` + timestamp + `</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">` + timestamp + `</dcterms:modified></cp:coreProperties>`
}

func appPropertiesXML(sheets []workbookSheet) string {
	var titles strings.Builder
	for _, sheet := range sheets {
		titles.WriteString(`<vt:lpstr>` + escapeXML(sheet.Name) + `</vt:lpstr>`)
	}
	return xmlHeader + `<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"><Application>Shopify Price Checker</Application><HeadingPairs><vt:vector size="2" baseType="variant"><vt:variant><vt:lpstr>Worksheets</vt:lpstr></vt:variant><vt:variant><vt:i4>` + strconv.Itoa(len(sheets)) + `</vt:i4></vt:variant></vt:vector></HeadingPairs><TitlesOfParts><vt:vector size="` + strconv.Itoa(len(sheets)) + `" baseType="lpstr">` + titles.String() + `</vt:vector></TitlesOfParts></Properties>`
}

func workbookXML(sheets []workbookSheet) string {
	var body strings.Builder
	for index, sheet := range sheets {
		fmt.Fprintf(&body, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, escapeXML(sheet.Name), index+1, index+1)
	}
	return xmlHeader + `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><bookViews><workbookView xWindow="0" yWindow="0" windowWidth="24000" windowHeight="12000"/></bookViews><sheets>` + body.String() + `</sheets><calcPr calcId="191029"/></workbook>`
}

func workbookRelationshipsXML(sheetCount int) string {
	var body strings.Builder
	for index := 1; index <= sheetCount; index++ {
		fmt.Fprintf(&body, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, index, index)
	}
	fmt.Fprintf(&body, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, sheetCount+1)
	return xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + body.String() + `</Relationships>`
}

func stylesXML(currency string) string {
	moneyFormat := `#,##0.00`
	if zeroDecimalCurrency(strings.ToUpper(currency)) {
		moneyFormat = `#,##0`
	}
	return xmlHeader + `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><numFmts count="2"><numFmt numFmtId="164" formatCode="` + moneyFormat + `"/><numFmt numFmtId="165" formatCode="0.00%"/></numFmts><fonts count="3"><font><sz val="11"/><name val="Calibri"/><family val="2"/></font><font><b/><color rgb="FFFFFFFF"/><sz val="11"/><name val="Calibri"/></font><font><u/><color rgb="FF2563EB"/><sz val="11"/><name val="Calibri"/></font></fonts><fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF17324D"/><bgColor indexed="64"/></patternFill></fill></fills><borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="5"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1" applyAlignment="1"><alignment vertical="center"/></xf><xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/><xf numFmtId="165" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/><xf numFmtId="0" fontId="2" fillId="0" borderId="0" xfId="0" applyFont="1"/></cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`
}

func zeroDecimalCurrency(currency string) bool {
	zero := map[string]bool{"BIF": true, "CLP": true, "DJF": true, "GNF": true, "ISK": true, "JPY": true, "KMF": true, "KRW": true, "PYG": true, "RWF": true, "UGX": true, "VND": true, "VUV": true, "XAF": true, "XOF": true, "XPF": true}
	return zero[currency]
}

func worksheetXML(sheet workbookSheet) (string, string) {
	maxColumns := 1
	for _, row := range sheet.Rows {
		if len(row) > maxColumns {
			maxColumns = len(row)
		}
	}
	maxRows := max(1, len(sheet.Rows))
	var columns strings.Builder
	for index, width := range sheet.Widths {
		fmt.Fprintf(&columns, `<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, index+1, index+1, width)
	}
	var data strings.Builder
	var hyperlinks strings.Builder
	var relationships strings.Builder
	relationID := 0
	for rowIndex, row := range sheet.Rows {
		fmt.Fprintf(&data, `<row r="%d">`, rowIndex+1)
		for columnIndex, cell := range row {
			ref := columnName(columnIndex+1) + strconv.Itoa(rowIndex+1)
			data.WriteString(cellXML(ref, cell))
			if cell.Hyperlink != "" {
				relationID++
				fmt.Fprintf(&hyperlinks, `<hyperlink ref="%s" r:id="rId%d"/>`, ref, relationID)
				fmt.Fprintf(&relationships, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="%s" TargetMode="External"/>`, relationID, escapeXML(cell.Hyperlink))
			}
		}
		data.WriteString(`</row>`)
	}
	filter := ""
	if len(sheet.Rows) > 1 {
		filter = `<autoFilter ref="A1:` + columnName(maxColumns) + strconv.Itoa(len(sheet.Rows)) + `"/>`
	}
	hyperlinkBlock, relationshipDoc := "", ""
	if relationID > 0 {
		hyperlinkBlock = `<hyperlinks>` + hyperlinks.String() + `</hyperlinks>`
		relationshipDoc = xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + relationships.String() + `</Relationships>`
	}
	content := xmlHeader + `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><dimension ref="A1:` + columnName(maxColumns) + strconv.Itoa(maxRows) + `"/><sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews><sheetFormatPr defaultRowHeight="15"/><cols>` + columns.String() + `</cols><sheetData>` + data.String() + `</sheetData>` + filter + hyperlinkBlock + `<pageMargins left="0.25" right="0.25" top="0.5" bottom="0.5" header="0.2" footer="0.2"/></worksheet>`
	return content, relationshipDoc
}

func cellXML(ref string, cell workbookCell) string {
	style := ""
	if cell.Style > 0 {
		style = ` s="` + strconv.Itoa(cell.Style) + `"`
	}
	switch value := cell.Value.(type) {
	case nil:
		return `<c r="` + ref + `"` + style + `/>`
	case int:
		return `<c r="` + ref + `"` + style + `><v>` + strconv.Itoa(value) + `</v></c>`
	case int64:
		return `<c r="` + ref + `"` + style + `><v>` + strconv.FormatInt(value, 10) + `</v></c>`
	case float64:
		return `<c r="` + ref + `"` + style + `><v>` + strconv.FormatFloat(value, 'f', -1, 64) + `</v></c>`
	case bool:
		if value {
			return `<c r="` + ref + `" t="b"` + style + `><v>1</v></c>`
		}
		return `<c r="` + ref + `" t="b"` + style + `><v>0</v></c>`
	default:
		return `<c r="` + ref + `" t="inlineStr"` + style + `><is><t xml:space="preserve">` + escapeXML(fmt.Sprint(value)) + `</t></is></c>`
	}
}

func columnName(index int) string {
	var result []byte
	for index > 0 {
		index--
		result = append([]byte{byte('A' + index%26)}, result...)
		index /= 26
	}
	return string(result)
}

func escapeXML(value string) string {
	var output bytes.Buffer
	xml.EscapeText(&output, []byte(value))
	return output.String()
}

func sortedCopy(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
