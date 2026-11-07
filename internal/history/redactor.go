package history

import "regexp"

// NewRedactor builds the same text filter without opening a logbook. Durable
// investigation storage must honor custom patterns even when history is disabled
// or its directory is unavailable. Invalid patterns prevent collection.
func NewRedactor(patterns []string) (func(string) string, error) {
	filter := &Store{}
	for _, pattern := range patterns {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		filter.patterns = append(filter.patterns, compiled)
	}
	return filter.Redact, nil
}
