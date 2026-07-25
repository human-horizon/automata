// Package slug provides URL/path-safe identifier generation.
package slug

import (
	"strings"
	"unicode"
)

// translit maps Cyrillic runes to Latin equivalents.
var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "yo",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch", 'ъ': "",
	'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
	'А': "A", 'Б': "B", 'В': "V", 'Г': "G", 'Д': "D", 'Е': "E", 'Ё': "Yo",
	'Ж': "Zh", 'З': "Z", 'И': "I", 'Й': "Y", 'К': "K", 'Л': "L", 'М': "M",
	'Н': "N", 'О': "O", 'П': "P", 'Р': "R", 'С': "S", 'Т': "T", 'У': "U",
	'Ф': "F", 'Х': "H", 'Ц': "Ts", 'Ч': "Ch", 'Ш': "Sh", 'Щ': "Sch", 'Ъ': "",
	'Ы': "Y", 'Ь': "", 'Э': "E", 'Ю': "Yu", 'Я': "Ya",
}

// Slug converts a string to a URL/path-safe identifier.
func Slug(s string) string {
	var b strings.Builder
	for _, r := range s {
		if mapped, ok := translit[r]; ok {
			b.WriteString(mapped)
			continue
		}
		switch {
		case r == ' ' || r == '_':
			b.WriteByte('-')
		case r == '/' || r == '\\' || r == '.':
			b.WriteByte('-')
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteByte('-')
		}
	}

	res := b.String()
	res = strings.ToLower(res)
	// Collapse consecutive separators.
	for strings.Contains(res, "--") {
		res = strings.ReplaceAll(res, "--", "-")
	}
	res = strings.Trim(res, "-")
	return res
}

// SessionName builds a session name from folder path and chat name.
// Folders are joined with dots, all lowercase, latin transliteration.
func SessionName(folders []string, chatName string) string {
	var parts []string
	for _, f := range folders {
		parts = append(parts, Slug(f))
	}
	parts = append(parts, Slug(chatName))
	return strings.Join(parts, ".")
}

// ComposeSessionID builds a session identifier from the folder path
// containing the chat and the chat name. All components are slugified and
// joined with dots. The current working directory is intentionally excluded
// so that session IDs are portable and match the format used by just-pi.
func ComposeSessionID(_ string, folders []string, chatName string) string {
	return SessionName(folders, chatName)
}
