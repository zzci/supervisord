package process

import (
	"path/filepath"
	"regexp"
)

type FileMatcher interface {
	// check if the file matches the expected
	Match(path string) bool
}

// if two files are exactly same
type ExactFileMatcher struct {
	filename string
}

func NewExactFileMatcher(filename string) *ExactFileMatcher {
	absName, err := filepath.Abs(filename)

	if err != nil {
		return nil
	}
	return &ExactFileMatcher{filename: absName}
}

func (efm *ExactFileMatcher) Match(path string) bool {
	if efm.filename == path {
		return true
	}
	absPath, err := filepath.Abs(path)
	return err == nil && absPath == efm.filename
}

// match the file basename by filepath.Match method
type PatternFileMatcher struct {
	pattern string
}

func NewPatternFileMatcher(pattern string) *PatternFileMatcher {
	return &PatternFileMatcher{pattern: pattern}
}

func (pfm *PatternFileMatcher) Match(path string) bool {
	matched, err := filepath.Match(pfm.pattern, filepath.Base(path))
	return matched && err == nil
}

type MatchAllFile struct {
}

func NewMatchAllFile() *MatchAllFile {
	return &MatchAllFile{}
}

func (maf MatchAllFile) Match(path string) bool {
	return true
}

// match by regular expression
type RegexFileMatcher struct {
	pattern *regexp.Regexp
}

func NewRegexFileMatcher(expr string) (*RegexFileMatcher, error) {
	r, err := regexp.Compile(expr)

	if err != nil {
		return nil, err
	}
	return &RegexFileMatcher{pattern: r}, nil
}

func (rfm *RegexFileMatcher) Match(path string) bool {
	return rfm.pattern.MatchString(path)
}

// match any of matcher
type AnyFileMatcher struct {
	fileMatchers []FileMatcher
}

func NewAnyFileMatcher(matchers ...FileMatcher) *AnyFileMatcher {
	r := &AnyFileMatcher{fileMatchers: make([]FileMatcher, 0)}
	for _, matcher := range matchers {
		r.fileMatchers = append(r.fileMatchers, matcher)
	}
	return r
}

func (afm *AnyFileMatcher) Match(path string) bool {
	for _, matcher := range afm.fileMatchers {
		if matcher.Match(path) {
			return true
		}
	}
	return false
}

// all the matchers should be matched
type AllFileMatcher struct {
	fileMatchers []FileMatcher
}

func NewAllFileMatcher(matchers ...FileMatcher) *AllFileMatcher {
	r := &AllFileMatcher{fileMatchers: make([]FileMatcher, 0)}
	for _, matcher := range matchers {
		r.fileMatchers = append(r.fileMatchers, matcher)
	}
	return r
}

func (afm *AllFileMatcher) Match(path string) bool {
	for _, matcher := range afm.fileMatchers {
		if !matcher.Match(path) {
			return false
		}
	}
	return true
}
