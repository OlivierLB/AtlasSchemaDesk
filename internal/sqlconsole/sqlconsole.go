// Package sqlconsole splits console scripts into statements, classifies them and
// lists the reasons that require an explicit confirmation. Pure functions only.
package sqlconsole

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/pyre"
)

// Statement kinds: read (no transaction needed), write (runs in the open
// transaction), ddl (structure; implicit commit on MySQL), session (SET/USE),
// control (refused: transactions use the console buttons).
const (
	Read    = "read"
	Write   = "write"
	DDL     = "ddl"
	Session = "session"
	Control = "control"
)

var (
	readWords    = set("SELECT", "WITH", "SHOW", "EXPLAIN", "DESCRIBE", "DESC", "VALUES", "TABLE", "HELP")
	ddlWords     = set("CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME", "GRANT", "REVOKE", "COMMENT", "ANALYZE", "OPTIMIZE", "REPAIR", "VACUUM", "REINDEX", "CLUSTER", "REFRESH", "SECURITY", "CHECKPOINT")
	sessionWords = set("SET", "USE", "RESET", "DISCARD", "PREPARE", "EXECUTE", "DEALLOCATE")
	controlWords = set("BEGIN", "START", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "END", "ABORT", "LOCK", "UNLOCK", "XA")

	routineDefinition = pyre.MustCompile(`(?i)^` + pyre.S + `*CREATE` + pyre.S + `+(?:OR` + pyre.S + `+REPLACE` + pyre.S + `+)?(?:DEFINER` + pyre.S + `*=` + pyre.S + `*` + pyre.NS + `+` + pyre.S + `+)?(?:PROCEDURE|FUNCTION|TRIGGER|EVENT)\b`)
	writeInWith       = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|MERGE)\b`)
	analyzeWord       = regexp.MustCompile(`(?i)\bANALYZE\b`)
	intoFile          = pyre.MustCompile(`(?i)\bINTO` + pyre.S + `+(OUTFILE|DUMPFILE)\b`)
	intoWord          = regexp.MustCompile(`(?i)\bINTO\b`)
	whereWord         = regexp.MustCompile(`(?i)\bWHERE\b`)
	asciiWord         = regexp.MustCompile(`[A-Za-z_]+`)
)

func set(words ...string) map[string]bool {
	result := make(map[string]bool, len(words))
	for _, word := range words {
		result[word] = true
	}
	return result
}

// MaskLiterals returns sql with the same number of runes, comments and
// string/identifier literals replaced by spaces (newlines kept).
func MaskLiterals(sql, engine string) string {
	return string(maskRunes([]rune(sql), engine))
}

func maskRunes(sql []rune, engine string) []rune {
	out := make([]rune, len(sql))
	copy(out, sql)
	length := len(sql)
	mysql := engine == "mysql"
	for i := 0; i < length; {
		char := sql[i]
		end := -1
		switch {
		case hasPrefix(sql, i, "--") || (char == '#' && mysql):
			end = indexFrom(sql, i, "\n")
			if end < 0 {
				end = length
			}
		case hasPrefix(sql, i, "/*"):
			end = indexFrom(sql, i+2, "*/")
			if end < 0 {
				end = length
			} else {
				end += 2
			}
		case char == '\'' || char == '"' || char == '`':
			j := i + 1
			for j < length {
				if sql[j] == '\\' && mysql && char != '`' {
					j += 2
					continue
				}
				if sql[j] == char {
					if j+1 < length && sql[j+1] == char {
						j += 2
						continue
					}
					break
				}
				j++
			}
			end = min(j+1, length)
		case char == '$' && !mysql:
			if tagLength := dollarTagLength(sql, i); tagLength > 0 {
				tag := string(sql[i : i+tagLength])
				close := indexFrom(sql, i+tagLength, tag)
				if close < 0 {
					end = length
				} else {
					end = close + tagLength
				}
			}
		}
		if end >= 0 {
			for k := i; k < end; k++ {
				if out[k] != '\n' {
					out[k] = ' '
				}
			}
			i = end
			continue
		}
		i++
	}
	return out
}

// dollarTagLength returns the rune length of a PostgreSQL dollar-quote tag
// ($$ or $name$) starting at i, or 0. Equivalent to \$([A-Za-z_]\w*)?\$.
func dollarTagLength(sql []rune, i int) int {
	j := i + 1
	if j < len(sql) && (sql[j] == '_' || (sql[j] < 128 && unicode.IsLetter(sql[j]))) {
		for j++; j < len(sql) && isWord(sql[j]); j++ {
		}
	}
	if j < len(sql) && sql[j] == '$' {
		return j - i + 1
	}
	return 0
}

func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

func hasPrefix(text []rune, at int, prefix string) bool {
	p := []rune(prefix)
	if at+len(p) > len(text) {
		return false
	}
	for k, r := range p {
		if text[at+k] != r {
			return false
		}
	}
	return true
}

func indexFrom(text []rune, from int, needle string) int {
	for k := max(from, 0); k < len(text); k++ {
		if hasPrefix(text, k, needle) {
			return k
		}
	}
	return -1
}

// Split splits on top-level semicolons. A MySQL routine/trigger/event
// definition (BEGIN … END with inner semicolons) is kept whole, since the
// console has no DELIMITER support.
func Split(sql, engine string) []string {
	if routineDefinition.MatchString(MaskLiterals(sql, engine)) {
		statement := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), ";"))
		if statement == "" {
			return []string{}
		}
		return []string{statement}
	}
	runes := []rune(sql)
	masked := maskRunes(runes, engine)
	var parts []string
	start := 0
	for index, char := range masked {
		if char == ';' {
			parts = append(parts, string(runes[start:index]))
			start = index + 1
		}
	}
	parts = append(parts, string(runes[start:]))
	statements := []string{}
	for _, part := range parts {
		if strings.TrimSpace(MaskLiterals(part, engine)) != "" {
			statements = append(statements, strings.TrimSpace(part))
		}
	}
	return statements
}

func firstWords(statement, engine string) []string {
	masked := strings.TrimLeft(MaskLiterals(statement, engine), " \n\t(")
	words := asciiWord.FindAllString(masked, 3)
	for index, word := range words {
		words[index] = strings.ToUpper(word)
	}
	return words
}

// Classify returns the statement kind.
func Classify(statement, engine string) string {
	words := firstWords(statement, engine)
	if len(words) == 0 {
		return Read
	}
	first := words[0]
	masked := MaskLiterals(statement, engine)
	switch {
	case first == "EXPLAIN":
		// EXPLAIN ANALYZE really executes the statement.
		if analyzeWord.MatchString(masked) && writeInWith.MatchString(masked) {
			return Write
		}
		return Read
	case first == "WITH":
		if writeInWith.MatchString(masked) {
			return Write
		}
		return Read
	case first == "SELECT" && intoFile.MatchString(masked):
		return Write
	case first == "SELECT" && engine != "mysql" && intoWord.MatchString(masked):
		return DDL
	case readWords[first]:
		return Read
	case controlWords[first]:
		return Control
	case ddlWords[first]:
		return DDL
	case sessionWords[first]:
		return Session
	}
	return Write // unknown: safest is to run it inside the transaction
}

// ConfirmationReasons explains why a statement needs explicit confirmation.
func ConfirmationReasons(statement, engine, kind string, transactionOpen bool) []string {
	masked := MaskLiterals(statement, engine)
	first := ""
	if words := firstWords(statement, engine); len(words) > 0 {
		first = words[0]
	}
	reasons := []string{}
	if (first == "UPDATE" || first == "DELETE") && !whereWord.MatchString(masked) {
		reasons = append(reasons, first+" sans WHERE : toutes les lignes de la table sont concernées")
	}
	if first == "DROP" {
		reasons = append(reasons, "DROP : supprime définitivement l'objet")
	}
	if first == "TRUNCATE" {
		reason := "TRUNCATE : vide la table"
		if engine == "mysql" {
			reason += " (validation immédiate, non annulable sous MySQL)"
		}
		reasons = append(reasons, reason)
	}
	if kind == DDL && engine == "mysql" {
		reason := "instruction de structure sous MySQL : validation implicite, non annulable"
		if transactionOpen {
			reason += " ; la transaction en cours sera validée avec elle"
		}
		reasons = append(reasons, reason)
	}
	return reasons
}

// Placeholders rewrites console "?" placeholders (outside literals) into
// PostgreSQL "$n" placeholders.
func Placeholders(sql, engine string) string {
	runes := []rune(sql)
	masked := maskRunes(runes, engine)
	var builder strings.Builder
	n := 0
	for index, char := range runes {
		if char == '?' && masked[index] == '?' {
			n++
			builder.WriteString("$")
			builder.WriteString(itoa(n))
			continue
		}
		builder.WriteRune(char)
	}
	return builder.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}
