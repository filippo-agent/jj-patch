# Pinned Unicode rendering data

`internal/prompt/unicode_tables.go` is generated from **Unicode 18.0.0**.
Its provenance header records versioned official URLs and SHA256 checksums;
`gen_unicode.py` enforces those checksums before parsing any input.
Neither builds nor runtime need Python, downloads, or Go's `unicode` tables.
Thus Go 1.24 and Go 1.27 classify even newly assigned characters identically.
The generated tables contain merged, sorted inclusive ranges, not copies of
the upstream data. Sparse confusable mappings require 1,976 ranges; the
non-displayable and combining-mark tables require 749 and 333.

## Display

The renderer interprets file text as UTF-8 and emits only its own fixed ANSI SGR
sequences. File-provided ESC, C1 controls, OSC/DCS sequences and invalid UTF-8
cannot pass through as terminal commands. LF and horizontal tabs are deliberate
layout exceptions; metadata tabs are escaped.

Non-displayable characters become ASCII escapes, warning-colored white on red.
Printable mapping sources retain their glyphs, warning-colored black on yellow.
After a warning, the renderer restores the line's addition/deletion/header
color and clears the warning background. Literal `\u0430` text remains literal
text, with no warning decoration.

Filenames and quoted command input are scanned as raw fields, separately from
their surrounding quotation syntax. This preserves warning provenance, escapes
filename newlines/tabs, and keeps a leading mark from attaching to a trusted
quote. Diagnostics, including flag-parser errors, also go through sanitization.

Displayable combining marks stay attached to their base. If a mark is
confusable, its whole base-plus-marks cluster gets the background so the warning
is visible. An orphan mark is escaped: it must not modify a trusted `+`/`-`
diff marker, a generated escape, or text on the preceding line. Default-ignorable
marks are always escaped. These are simple mark clusters, not a full shaping
engine; the renderer cannot inspect a terminal's fonts.

All ANSI decoration is disabled when output is not a terminal, `TERM=dumb`, or
`NO_COLOR` is set. Escaping still applies; printable confusables stay unescaped.
The external program chosen by `$VISUAL`/`$EDITOR` remains responsible for its
own terminal display.

## Policy

* `isConfusable` flags **non-ASCII source code points** with a mapping in
  UTS #39 `confusables.txt`. Mapping targets are not independently flagged.
  For example, Cyrillic `а`, Greek `Α`, fullwidth `Ａ`, and script `ℓ` are
  flagged; ASCII `a` and `m` are not, even though `m` has a source mapping.
  Greek `μ` is not flagged merely because MICRO SIGN maps to it.
  This is a conservative, context-free warning, not normalization, a complete
  implementation of the UTS #39 skeleton algorithm, or perfect font-aware
  confusable detection. Some non-Latin letters and ordinary marks are mapping
  sources; this policy does not imply that those scripts are unsafe.
* `isNonDisplayable` includes all categories outside **L/M/N/P/S**, except
  U+0020 ASCII SPACE, plus `Default_Ignorable_Code_Point` and the explicit
  blanks below. Invalid runes, surrogates, private-use characters, unassigned
  code points and noncharacters are therefore included. Tabs and newlines
  return true here; any permitted layout exceptions belong to the renderer.
* `isCombiningMark` recognizes pinned categories Mn/Mc/Me, allowing the
  renderer to handle attached marks without relying on Go's Unicode version.
  Ordinary assigned combining marks are **not** non-displayable merely for
  being marks. Default-ignorable marks such as U+034F, U+FE0F and U+E0100 are.

### Assigned blanks and fillers

Default-ignorable data already handles the Hangul fillers U+115F, U+1160,
U+3164 and U+FFA0, Khmer inherent vowels U+17B4/U+17B5, variation selectors,
joiners and bidi controls. Category filtering also catches all non-ASCII
space separators and format characters, even those excluded from the
default-ignorable property.

These assigned blanks are **not** default-ignorable, so the generator adds
them explicitly:

| Code point | Unicode name | Category |
| --- | --- | --- |
| U+2800 | BRAILLE PATTERN BLANK | So |
| U+13441 | EGYPTIAN HIEROGLYPH FULL BLANK | Lo |
| U+13442 | EGYPTIAN HIEROGLYPH HALF BLANK | Lo |
| U+16FE4 | KHITAN SMALL SCRIPT FILLER | Mn |

The Egyptian blank signs and Khitan format filler are identified in the
official code charts `https://www.unicode.org/charts/PDF/U13430.pdf` and
`https://www.unicode.org/charts/PDF/U16FE0.pdf`. Visible punctuation called
"gap filler", and U+2422 BLANK SYMBOL, are not blanket-excluded by name.
This is an explicit safety policy, not a claim that Unicode properties
fully predict glyph visibility in every font or shaping context.

## Regeneration

From the repository root, run:

```sh
python3 internal/prompt/gen_unicode.py
python3 internal/prompt/gen_unicode.py --check
go test ./internal/prompt -run TestPinnedUnicode
```

The first two commands download the three versioned data files and Unicode
license, verifying every SHA256. For an entirely offline invocation, add
`--data-dir /path/to/data` containing `confusables.txt`, `UnicodeData.txt`,
`DerivedCoreProperties.txt` and `license.txt`; checksums are still mandatory.
`--check` compares both generated Go and the license without changing either.
Only maintainers regenerating data require Python's standard library.

When upgrading, review official versioned data and the blank exceptions,
update the version and checksums in the generator, regenerate, and update
the exhaustive classification digests and version-specific test fixtures.
The license endpoint is not versioned, but its contents are checksum-pinned;
a changed license intentionally requires review rather than silent acceptance.
Unicode data's copyright and permission notice is in
`licenses/Unicode-3.0.txt` (Unicode License V3).
