package main

import "testing"

func TestFoldAndTitleMatchVietnamese(t *testing.T) {
	if got := foldText("  Điện THOẠI  Đỏ "); got != "dien thoai do" {
		t.Fatalf("foldText() = %q", got)
	}
	if !titleMatches("Ốp lưng Điện Thoại iPhone 16 Pro", []string{"dien thoai iphone 16"}) {
		t.Fatal("expected accent-insensitive match")
	}
	if titleMatches("MacBook Air", []string{"iPhone", "iPad"}) {
		t.Fatal("unexpected OR phrase match")
	}
}

func TestCleanPhrases(t *testing.T) {
	got := cleanPhrases([]string{"iPhone 16, iPad", "iphone 16\nMacBook"})
	if len(got) != 3 {
		t.Fatalf("cleanPhrases() length = %d, want 3: %#v", len(got), got)
	}
}

func TestPlainTextKeepsNaturalPunctuation(t *testing.T) {
	got := plainText(`<p>Điện thoại <strong>mới</strong>.</p><p>Giá tốt!</p>`)
	if got != "Điện thoại mới. Giá tốt!" {
		t.Fatalf("plainText=%q", got)
	}
}
