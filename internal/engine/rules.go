package engine

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ApplyRules runs the rule chain against a single file name and returns the
// resulting name. name is split into stem + extension up front; every rule
// except "extension" operates on the stem only, so the extension is never
// touched unless the user explicitly asked for that with an extension rule.
// seqIndex is this file's position (0-based) within the current batch, used
// by the sequence rule; modTime is the file's on-disk modification time,
// used by the date rule when DateSource is "modified".
func ApplyRules(name string, rules []Rule, seqIndex int, modTime time.Time) (string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)

	for _, rule := range rules {
		var err error
		switch rule.Type {
		case "find-replace":
			stem = applyFindReplace(stem, rule)
		case "regex-replace":
			stem, err = applyRegexReplace(stem, rule)
		case "sequence":
			stem = applySequence(stem, rule, seqIndex)
		case "date":
			stem = applyDate(stem, rule, modTime)
		case "case":
			stem = applyCase(stem, rule)
		case "trim":
			stem = applyTrim(stem, rule)
		case "remove-chars":
			stem = applyRemoveChars(stem, rule)
		case "extension":
			ext = applyExtension(rule)
		default:
			// Unknown rule types are ignored rather than erroring, so a
			// preview never fails outright over one bad/future rule.
		}
		if err != nil {
			return "", err
		}
	}

	return stem + ext, nil
}

func applyFindReplace(stem string, rule Rule) string {
	if rule.Find == "" {
		return stem
	}
	if rule.CaseSensitive {
		return strings.ReplaceAll(stem, rule.Find, rule.Replace)
	}
	return replaceAllFold(stem, rule.Find, rule.Replace)
}

// replaceAllFold is a case-insensitive strings.ReplaceAll: stdlib has no
// such function, so this scans stem for occurrences of find compared via
// strings.EqualFold and splices in replace at each match, preserving
// everything else verbatim (including the original casing of any part of
// stem that isn't matched).
func replaceAllFold(stem, find, replace string) string {
	if find == "" {
		return stem
	}
	var b strings.Builder
	lowerFind := strings.ToLower(find)
	lowerStem := strings.ToLower(stem)
	i := 0
	for i < len(stem) {
		idx := strings.Index(lowerStem[i:], lowerFind)
		if idx < 0 {
			b.WriteString(stem[i:])
			break
		}
		matchStart := i + idx
		b.WriteString(stem[i:matchStart])
		b.WriteString(replace)
		i = matchStart + len(find)
	}
	return b.String()
}

func applyRegexReplace(stem string, rule Rule) (string, error) {
	if rule.Find == "" {
		return stem, nil
	}
	re, err := regexp.Compile(rule.Find)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidRegex, err)
	}
	return re.ReplaceAllString(stem, rule.Replace), nil
}

func applySequence(stem string, rule Rule, seqIndex int) string {
	step := rule.Step
	if step == 0 {
		step = 1
	}
	padding := rule.Padding
	if padding < 1 {
		padding = 1
	}
	n := rule.Start + seqIndex*step
	num := fmt.Sprintf("%0*d", padding, n)
	return insert(stem, num, rule.Position, rule.Separator)
}

func applyDate(stem string, rule Rule, modTime time.Time) string {
	source := modTime
	if rule.DateSource == "today" {
		source = time.Now()
	}
	layout := rule.DateFormat
	if layout == "" {
		layout = "2006-01-02"
	}
	return insert(stem, source.Format(layout), rule.Position, rule.Separator)
}

// insert splices text into stem at the prefix or suffix (default: suffix),
// joined by separator.
func insert(stem, text, position, separator string) string {
	if position == "prefix" {
		return text + separator + stem
	}
	return stem + separator + text
}

func applyCase(stem string, rule Rule) string {
	switch rule.CaseMode {
	case "lower":
		return strings.ToLower(stem)
	case "upper":
		return strings.ToUpper(stem)
	case "title":
		return titleCase(stem)
	default:
		return stem
	}
}

// titleCase capitalizes the first letter of each word, where a word is a
// run of characters separated by spaces, hyphens, or underscores — no
// external dependency needed for something this small.
func titleCase(s string) string {
	isSep := func(r rune) bool { return r == ' ' || r == '-' || r == '_' }
	runes := []rune(s)
	atWordStart := true
	for i, r := range runes {
		if isSep(r) {
			atWordStart = true
			continue
		}
		if atWordStart {
			runes[i] = []rune(strings.ToUpper(string(r)))[0]
			atWordStart = false
		} else {
			runes[i] = []rune(strings.ToLower(string(r)))[0]
		}
	}
	return string(runes)
}

func applyTrim(stem string, rule Rule) string {
	if rule.Chars == "" {
		return strings.TrimSpace(stem)
	}
	return strings.Trim(stem, rule.Chars)
}

func applyRemoveChars(stem string, rule Rule) string {
	if rule.Chars == "" {
		return stem
	}
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(rule.Chars, r) {
			return -1
		}
		return r
	}, stem)
}

func applyExtension(rule Rule) string {
	ext := strings.TrimPrefix(rule.NewExtension, ".")
	if ext == "" {
		return ""
	}
	return "." + ext
}
