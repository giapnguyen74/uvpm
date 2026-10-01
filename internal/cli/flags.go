package cli

import (
	"flag"
	"strings"
)

// strList is a repeatable string flag.
type strList []string

func (s *strList) String() string     { return strings.Join(*s, ",") }
func (s *strList) Set(v string) error { *s = append(*s, v); return nil }

// parseInterspersed parses flags that may appear before or after positional
// arguments. Everything after a bare "--" is returned separately, untouched.
func parseInterspersed(fs *flag.FlagSet, args []string) (pos, after []string, err error) {
	for i, a := range args {
		if a == "--" {
			args, after = args[:i], args[i+1:]
			break
		}
	}
	for {
		if err := fs.Parse(args); err != nil {
			return nil, nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, after, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}
