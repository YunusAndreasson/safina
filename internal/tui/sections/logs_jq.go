package sections

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/itchyny/gojq"
)

// jq is an opt-in predicate: any output other than false/null keeps the raw
// line. Compilation is cached across stream chunks. No modules, environment,
// filesystem access, or external input are supplied to the interpreter.
type logJQ struct {
	query string
	code  *gojq.Code
	err   error
}

func (j *logJQ) prepare(query string) {
	if j.query == query {
		return
	}
	*j = logJQ{query: query}
	parsed, err := gojq.Parse(strings.TrimSpace(strings.TrimPrefix(query, "jq:")))
	if err == nil {
		j.code, err = gojq.Compile(parsed)
	}
	if err != nil {
		j.err = errors.New("jq: invalid expression")
	}
}

func (j *logJQ) matches(ctx context.Context, line string) (bool, error) {
	// Kubernetes supplies an RFC3339 timestamp before the application's JSON.
	if prefix, rest, ok := strings.Cut(line, " "); ok {
		if _, err := time.Parse(time.RFC3339Nano, prefix); err == nil {
			line = rest
		}
	}
	if len(line) > 256<<10 {
		return false, nil
	}
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	var value any
	// Non-JSON application logs are valid input, simply not jq matches.
	if !json.Valid([]byte(line)) {
		return false, nil
	}
	if err := dec.Decode(&value); err != nil {
		return false, err
	}
	iter := j.code.RunWithContext(ctx, value)
	// Bound false/empty-result generators as well as CPU work. Returning after
	// the first truthy value also makes `select(...)` useful as a predicate.
	for range 256 {
		v, ok := iter.Next()
		if !ok {
			return false, nil
		}
		if _, ok = v.(error); ok {
			return false, errors.New("jq: evaluation failed or time limit exceeded")
		}
		if v != nil {
			if b, ok := v.(bool); !ok || b {
				return true, nil
			}
		}
	}
	return false, errors.New("jq: output limit exceeded")
}

func (l *Logs) appendJQ(start int, query string) {
	l.jq.prepare(query)
	if l.jq.err != nil {
		return
	}
	// One budget for the whole batch, not one timeout per line. A pathological
	// query cannot multiply its allowance by the size of the log backlog.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	for i := start; i < len(l.lines); i++ {
		if ctx.Err() != nil {
			l.jq.err = errors.New("jq: time limit exceeded; narrow the query")
			break
		}
		matched, err := l.jq.matches(ctx, l.lines[i])
		if err != nil {
			l.jq.err = err
			break
		}
		if matched {
			l.filtered = append(l.filtered, i)
		}
	}
	if l.jq.err != nil {
		l.filtered = l.filtered[:0]
	}
}
