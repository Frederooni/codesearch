package index

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Ignorer decides which paths a walk of one indexed path skips, from the
// exclude patterns and the ignore files in the directories above each path.
type Ignorer struct {
	// root is the indexed path. Exclude patterns that start with / are
	// relative to it.
	root string
	// exclude holds the exclude patterns, which override the ignore files.
	exclude   IgnoreRules
	gitignore bool
	// dirs runs from root down to the directory holding the visited path.
	dirs []ignoreDir
}

// NewIgnorer returns an Ignorer for a walk of root. It always reads
// .csearch.ignore files, and also .gitignore files if gitignore is set.
func NewIgnorer(root string, exclude IgnoreRules, gitignore bool) *Ignorer {
	return &Ignorer{root: root, exclude: exclude, gitignore: gitignore}
}

// ignoreDir holds the rules from the ignore files in one directory.
type ignoreDir struct {
	path  string
	files []IgnoreRules
}

// IgnoreRules holds the patterns from an exclude list or an ignore file.
type IgnoreRules []ignoreRule

// ignoreRule is one pattern from an exclude list or an ignore file.
type ignoreRule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
	// base patterns match the last path element instead of the whole path.
	base bool
}

// relTo returns path, which is inside dir, relative to dir.
func relTo(dir, path string) string {
	return strings.TrimPrefix(path[len(dir):], string(filepath.Separator))
}

// Leave drops the directories that do not hold logical.
func (s *Ignorer) Leave(logical string) {
	parent := filepath.Dir(logical)
	for len(s.dirs) > 0 && s.dirs[len(s.dirs)-1].path != parent {
		s.dirs = s.dirs[:len(s.dirs)-1]
	}
}

// Enter reads the ignore files in the directory at path, which the walk
// reports as logical.
func (s *Ignorer) Enter(path, logical string) {
	d := ignoreDir{path: logical}
	if s.gitignore {
		d.read(filepath.Join(path, ".gitignore"))
	}
	d.read(filepath.Join(path, ".csearch.ignore"))
	s.dirs = append(s.dirs, d)
}

func (d *ignoreDir) read(file string) {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("%s: %s", file, err)
		}
		return
	}
	if rules := parseIgnore(string(data)); len(rules) > 0 {
		d.files = append(d.files, rules)
	}
}

// Ignored reports whether the exclude patterns or the ignore files above
// logical exclude it. elem is the name the walk found it under, which exclude
// name patterns match. As in git, later rules override earlier ones and
// deeper files override shallower ones.
func (s *Ignorer) Ignored(logical, elem string, isDir bool) bool {
	rel := filepath.ToSlash(relTo(s.root, logical))
	if matched, ignore := s.exclude.match(rel, elem, isDir); matched {
		return ignore
	}
	for i := len(s.dirs) - 1; i >= 0; i-- {
		d := s.dirs[i]
		if len(d.files) == 0 {
			continue
		}
		rel := filepath.ToSlash(relTo(d.path, logical))
		base := rel[strings.LastIndexByte(rel, '/')+1:]
		for j := len(d.files) - 1; j >= 0; j-- {
			if matched, ignore := d.files[j].match(rel, base, isDir); matched {
				return ignore
			}
		}
	}
	return false
}

// match reports whether a rule matches rel, a slash-separated path relative
// to the rules' directory, or base, its last element, and whether the last
// such rule ignores it.
func (rules IgnoreRules) match(rel, base string, isDir bool) (matched, ignore bool) {
	for i := len(rules) - 1; i >= 0; i-- {
		r := rules[i]
		if r.dirOnly && !isDir {
			continue
		}
		name := base
		if !r.base {
			// A path pattern never matches the directory it is relative to.
			if rel == "" {
				continue
			}
			name = rel
		}
		if r.re.MatchString(name) {
			return true, !r.negate
		}
	}
	return false, false
}

// ParseExclude parses exclude patterns, which use filepath.Match syntax and
// match a name, or with a leading / a path below the indexed path.
func ParseExclude(patterns []string) (IgnoreRules, error) {
	var rules IgnoreRules
	for _, p := range patterns {
		if p == "" {
			continue
		}
		r := ignoreRule{base: !strings.HasPrefix(p, "/")}
		expr, ok := globRegexp(strings.TrimPrefix(p, "/"), false)
		var err error
		if ok {
			r.re, err = regexp.Compile("^" + expr + "$")
		}
		if !ok || err != nil {
			return nil, fmt.Errorf("%s: %s", p, filepath.ErrBadPattern)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// parseIgnore parses the contents of a file in .gitignore syntax. Invalid
// patterns are dropped, as git does.
func parseIgnore(data string) IgnoreRules {
	var rules IgnoreRules
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSuffix(line, "\r")
		for strings.HasSuffix(line, " ") && !strings.HasSuffix(line, `\ `) {
			line = line[:len(line)-1]
		}
		if line == "" || line[0] == '#' {
			continue
		}
		var r ignoreRule
		if line[0] == '!' {
			r.negate = true
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly = true
			line = line[:len(line)-1]
		}
		if line == "" {
			continue
		}
		var expr string
		if strings.Contains(line, "/") {
			expr = pathRegexp(strings.TrimPrefix(line, "/"))
		} else {
			r.base = true
			glob, _ := globRegexp(line, true)
			expr = "^" + glob + "$"
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			continue
		}
		r.re = re
		rules = append(rules, r)
	}
	return rules
}

// pathRegexp converts a pattern that holds a slash, minus any leading slash,
// to a regexp for paths relative to the ignore file's directory.
func pathRegexp(pattern string) string {
	segs := strings.Split(pattern, "/")
	var b strings.Builder
	b.WriteString("^")
	for i, seg := range segs {
		last := i == len(segs)-1
		switch {
		case seg == "**" && last:
			b.WriteString(".*")
		case seg == "**":
			b.WriteString("(?:.*/)?")
		default:
			glob, _ := globRegexp(seg, true)
			b.WriteString(glob)
			if !last {
				b.WriteString("/")
			}
		}
	}
	b.WriteString("$")
	return b.String()
}

// globRegexp converts a glob in .gitignore syntax if git is set, or else in
// filepath.Match syntax, to a regexp. It reports false for a filepath.Match
// glob that filepath.Match would reject.
func globRegexp(glob string, git bool) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		switch glob[i] {
		case '*':
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '[':
			if class, n := classRegexp(glob[i:], git); n > 0 {
				b.WriteString(class)
				i += n - 1
			} else if git {
				b.WriteString(`\[`)
			} else {
				return "", false
			}
		case '\\':
			if i+1 < len(glob) {
				i++
			} else if !git {
				return "", false
			}
			fallthrough
		default:
			b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
		}
	}
	return b.String(), true
}

// classRegexp converts the bracket expression at the start of glob to a
// regexp character class and returns it with the expression's length, or
// returns a length of 0 if the expression is not closed. Only .gitignore
// syntax negates with ! and has [:name:] classes.
func classRegexp(glob string, git bool) (string, int) {
	var b strings.Builder
	b.WriteString("[")
	i := 1
	if i < len(glob) && (glob[i] == '^' || git && glob[i] == '!') {
		// A negated class never matches a slash.
		b.WriteString("^/")
		i++
	}
	for start := i; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == ']' && i > start:
			b.WriteString("]")
			return b.String(), i + 1
		case git && c == '[' && strings.HasPrefix(glob[i:], "[:"):
			if j := strings.Index(glob[i+2:], ":]"); j >= 0 {
				b.WriteString(glob[i : i+j+4])
				i += j + 3
			} else {
				b.WriteString(`\[`)
			}
		case c == '\\' && i+1 < len(glob):
			i++
			if glob[i] == '-' {
				b.WriteString(`\-`)
			} else {
				b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
			}
		case c == ']' || c == '[' || c == '^' || c == '\\':
			b.WriteString(`\`)
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return "", 0
}
