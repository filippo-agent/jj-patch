package prompt

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestPinnedUnicodeConfusables(t *testing.T) {
	for _, r := range []rune{'\u0430', '\u0391', '\uFF21', '\u2113', '\U0001D400', '\u00B5', '\u093F'} {
		if !isConfusable(r) {
			t.Errorf("mapping source %U not flagged", r)
		}
	}
	for r := rune(0); r < 128; r++ {
		if isConfusable(r) {
			t.Errorf("ASCII %U flagged (including source m and target a)", r)
		}
	}
	// Greek mu is a mapping target (e.g. MICRO SIGN -> mu), not a source.
	for _, r := range []rune("中文你好世界éμ\u0301\uFFFD") {
		if isConfusable(r) {
			t.Errorf("non-source %U flagged", r)
		}
	}
	for _, r := range []rune{-1, 0xD800, 0x110000} {
		if isConfusable(r) {
			t.Errorf("invalid rune %U flagged as confusable", r)
		}
	}
}

func TestPinnedUnicodeNonDisplayable(t *testing.T) {
	for _, r := range []rune{
		0, '\t', '\n', '\r', 0x1B, 0x7F, 0x85, // renderer exempts tabs/newlines
		0xA0, 0x1680, 0x2000, 0x2007, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000,
		0xAD, 0x034F, 0x061C, 0x115F, 0x1160, 0x17B4, 0x17B5, 0x180B,
		0x200B, 0x200C, 0x200D, 0x202E, 0x2060, 0x2066, 0x3164, 0xFE0F,
		0xFEFF, 0xFFA0, 0xFFF9, 0xE0001, 0xE007F, 0xE0100,
		0x2800, 0x13441, 0x13442, 0x16FE4, // explicit assigned blank exceptions
		-1, 0xD800, 0xDFFF, 0xE000, 0xF0000, 0x100000, // invalid/private use
		0x0378, 0xFDD0, 0xFFFE, 0xFFFF, 0x10FFFF, 0x110000, // unassigned/noncharacters
	} {
		if !isNonDisplayable(r) {
			t.Errorf("unsafe/invisible %U considered displayable", r)
		}
	}
	for _, r := range []rune(" aAm0!中文你好é\u0301\u093F\u0430\u0391\uFFFD\U0001F600") {
		if isNonDisplayable(r) {
			t.Errorf("ordinary letter/mark/number/punctuation/symbol %U escaped", r)
		}
	}
	for r := rune(0x21); r <= 0x7E; r++ {
		if isNonDisplayable(r) {
			t.Errorf("printable ASCII %U escaped", r)
		}
	}
}

func TestPinnedUnicode18Assignments(t *testing.T) {
	// Newly assigned in 18.0.0: L, M, N, P, S and First/Last range endpoints.
	// These must not depend on the Go toolchain's older unicode tables.
	for _, r := range []rune{0x0558, 0x05C8, 0x1ADE, 0x20C2, 0x2E60, 0x1246F, 0x1F6D9, 0x3D000, 0x3FC3F} {
		if isNonDisplayable(r) {
			t.Errorf("Unicode 18 assignment %U escaped", r)
		}
	}
	if !isNonDisplayable(0x3FC40) {
		t.Error("unassigned code point after Unicode 18 Small Seal range is displayable")
	}
}

func TestPinnedUnicodeCombiningMarks(t *testing.T) {
	for _, r := range []rune{0x0301, 0x093F, 0x20DD, 0x05C8, 0x1ADE} {
		if !isCombiningMark(r) || isNonDisplayable(r) {
			t.Errorf("ordinary assigned mark %U not recognized as displayable", r)
		}
	}
	for _, r := range []rune{-1, 'a', '中', 0x0558, 0xD800, 0x110000} {
		if isCombiningMark(r) {
			t.Errorf("non-mark %U recognized as a combining mark", r)
		}
	}
}

func TestPinnedUnicodeRanges(t *testing.T) {
	for name, ranges := range map[string][]unicodeRange{
		"confusable": confusableRanges[:], "non-displayable": nonDisplayableRanges[:],
		"combining-mark": combiningMarkRanges[:],
	} {
		for i, span := range ranges {
			if span.lo < 0 || span.hi > 0x10FFFF || span.lo > span.hi {
				t.Fatalf("%s: invalid range %#v", name, span)
			}
			if i > 0 && ranges[i-1].hi+1 >= span.lo {
				t.Fatalf("%s: unsorted, overlapping, or unmerged range %#v", name, span)
			}
		}
	}
}

func TestPinnedUnicodeFullClassification(t *testing.T) {
	// SHA256 of one byte per code point, 0=false/1=true; includes every hole
	// and boundary. Changing the pin/policy requires updating these fixtures.
	for _, tc := range []struct {
		name string
		pred func(rune) bool
		want string
	}{
		{"confusable", isConfusable, "0bb940b157a5546b69734488abda29804a06cf2d27e8818cdbdaf75b6055c5d9"},
		{"non-displayable", isNonDisplayable, "56af67e15fc47d01d2fe5aa17aa111243b0e823ce833a69adcdedc73eff1f915"},
		{"combining-mark", isCombiningMark, "1cd5644acd29c06836a09514d749cbd0efe9beafb6371414c163dcf6dcb93652"},
	} {
		bits := make([]byte, 0x110000)
		for r := range bits {
			if tc.pred(rune(r)) {
				bits[r] = 1
			}
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(bits)); got != tc.want {
			t.Errorf("%s classification SHA256 = %s, want %s", tc.name, got, tc.want)
		}
	}
}
