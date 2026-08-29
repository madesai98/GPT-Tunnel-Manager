package downstream

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func (f *Factory) resolveEnvironment(ctx context.Context, server v2config.ServerEntry) ([]string, []string, error) {
	values := make(map[string]string, len(server.Environment.Values)+len(server.Environment.SecretRefs))
	for name, value := range server.Environment.Values {
		values[name] = value
	}
	redactions := make([]string, 0, len(server.Environment.SecretRefs))
	for name, ref := range server.Environment.SecretRefs {
		secret, err := f.secrets.Get(ctx, ref)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve environment secret %s for %s: %w", ref, server.ID, err)
		}
		value := string(secret)
		if strings.IndexByte(value, 0) >= 0 {
			return nil, nil, fmt.Errorf("environment secret %s for %s contains NUL", ref, server.ID)
		}
		values[name] = value
		if value != "" {
			redactions = append(redactions, value)
		}
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	env := make([]string, 0, len(names))
	for _, name := range names {
		env = append(env, name+"="+values[name])
	}
	return env, redactions, nil
}

func (f *Factory) emitLog(serverID, stream, text string, redactions []string) {
	if f.log == nil {
		return
	}
	line := text
	for _, secret := range redactions {
		if secret != "" {
			line = strings.ReplaceAll(line, secret, "[REDACTED]")
		}
	}
	defer func() { _ = recover() }()
	f.log(LogLine{ServerID: serverID, Stream: stream, Text: line})
}

func scanLines(reader io.Reader, fn func(string)) {
	if reader == nil || fn == nil {
		return
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		func() {
			defer func() { _ = recover() }()
			fn(scanner.Text())
		}()
	}
}
