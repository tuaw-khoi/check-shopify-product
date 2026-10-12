package main

import (
	"html"
	"regexp"
	"sort"
	"strings"
)

var vietnameseFolder = strings.NewReplacer(
	"à", "a", "á", "a", "ạ", "a", "ả", "a", "ã", "a", "â", "a", "ầ", "a", "ấ", "a", "ậ", "a", "ẩ", "a", "ẫ", "a", "ă", "a", "ằ", "a", "ắ", "a", "ặ", "a", "ẳ", "a", "ẵ", "a",
	"è", "e", "é", "e", "ẹ", "e", "ẻ", "e", "ẽ", "e", "ê", "e", "ề", "e", "ế", "e", "ệ", "e", "ể", "e", "ễ", "e",
	"ì", "i", "í", "i", "ị", "i", "ỉ", "i", "ĩ", "i",
	"ò", "o", "ó", "o", "ọ", "o", "ỏ", "o", "õ", "o", "ô", "o", "ồ", "o", "ố", "o", "ộ", "o", "ổ", "o", "ỗ", "o", "ơ", "o", "ờ", "o", "ớ", "o", "ợ", "o", "ở", "o", "ỡ", "o",
	"ù", "u", "ú", "u", "ụ", "u", "ủ", "u", "ũ", "u", "ư", "u", "ừ", "u", "ứ", "u", "ự", "u", "ử", "u", "ữ", "u",
	"ỳ", "y", "ý", "y", "ỵ", "y", "ỷ", "y", "ỹ", "y", "đ", "d",
)

func foldText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = vietnameseFolder.Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func cleanPhrases(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, raw := range values {
		for _, value := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ';' }) {
			value = strings.TrimSpace(value)
			key := foldText(value)
			if key != "" && !seen[key] {
				seen[key] = true
				result = append(result, value)
			}
		}
	}
	return result
}

func titleMatches(title string, phrases []string) bool {
	if len(phrases) == 0 {
		return true
	}
	folded := foldText(title)
	for _, phrase := range phrases {
		if strings.Contains(folded, foldText(phrase)) {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	seen := map[string]string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; !ok {
			seen[key] = value
		}
	}
	result := make([]string, 0, len(seen))
	for _, value := range seen {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return foldText(result[i]) < foldText(result[j]) })
	return result
}

var tagExpression = regexp.MustCompile(`(?s)<[^>]*>`)
var spaceBeforePunctuation = regexp.MustCompile(`\s+([,.;:!?…])`)

func plainText(value string) string {
	value = strings.Join(strings.Fields(html.UnescapeString(tagExpression.ReplaceAllString(value, " "))), " ")
	return spaceBeforePunctuation.ReplaceAllString(value, "$1")
}
