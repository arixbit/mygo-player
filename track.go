package player

import (
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// DisplayName returns a short human-readable name for the track, suitable
// for menus: the language spelled out ("Japanese" for "jpn" or "ja"), the
// track title when it adds something, and "Forced" for forced subtitles,
// joined with " · ". External subtitle files without a meaningful title fall
// back to their file name. Tracks with no language, title, or file name are
// named "Track N" by their libmpv ID. Codec and ID details are left out on
// purpose; callers that show several tracks with the same name can append
// them to tell the tracks apart.
func (t Track) DisplayName() string {
	var parts []string
	lang := LanguageName(t.Lang)
	if lang != "" {
		parts = append(parts, lang)
	}
	title := strings.TrimSpace(t.Title)
	file, raw := "", ""
	if t.External && t.ExternalFilename != "" {
		file, raw = externalName(t.ExternalFilename)
	}
	// libmpv titles external subtitles by their file name, or the part of
	// it that differs from the video ("srt", "en.srt"), which is noise;
	// for a URL it includes the query, which may carry credentials.
	if file != "" && (title == "" || strings.HasSuffix(strings.ToLower(raw), strings.ToLower(title)) ||
		strings.Contains(t.ExternalFilename, "://") && inURL(title, t.ExternalFilename)) {
		title = file
	}
	if title != "" && !strings.EqualFold(title, lang) && !strings.EqualFold(title, t.Lang) {
		parts = append(parts, title)
	}
	if t.Forced {
		parts = append(parts, "Forced")
	}
	if len(parts) == 0 {
		return "Track " + strconv.FormatInt(t.ID, 10)
	}
	return strings.Join(parts, " · ")
}

// externalName returns the name to show for an external track's file, and
// the raw last element libmpv derives titles from. For URLs the name is the
// last element of the path alone, without query or fragment.
func externalName(name string) (shown, raw string) {
	if strings.Contains(name, "://") {
		raw = name[strings.LastIndex(name, "/")+1:]
		if u, err := url.Parse(name); err == nil { // Path is decoded
			if b := path.Base(u.Path); b != "." && b != "/" {
				return b, raw
			}
			return u.Host, raw
		}
		if i := strings.IndexAny(raw, "?#"); i >= 0 {
			return raw[:i], raw
		}
		return raw, raw
	}
	b := filepath.Base(name)
	return b, b
}

// inURL reports whether title is part of u as written or percent-decoded,
// as libmpv's titles for URLs are.
func inURL(title, u string) bool {
	if strings.Contains(u, title) {
		return true
	}
	if d, err := url.QueryUnescape(u); err == nil && strings.Contains(d, title) {
		return true
	}
	d, err := url.PathUnescape(u)
	return err == nil && strings.Contains(d, title)
}

// LanguageName returns the English name of an ISO 639-1 ("ja") or ISO 639-2
// ("jpn"; both "ger" and "deu") language code as reported in track
// metadata, ignoring case and region suffixes ("pt-BR" is "Portuguese").
// It knows about fifty widely used languages; other codes are returned
// upper-cased ("BN"), which still reads as a language code. Codes that name
// no single language ("und", "mul", "mis", "zxx") and empty codes return "",
// so that callers fall back to the track's title.
func LanguageName(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	if i := strings.IndexAny(c, "-_"); i > 0 {
		c = c[:i]
	}
	switch c {
	case "", "und", "unk", "mis", "zxx", "mul":
		return ""
	}
	if name, ok := languageNames[c]; ok {
		return name
	}
	return strings.ToUpper(c)
}

var languageNames = map[string]string{
	"ar": "Arabic", "ara": "Arabic",
	"bg": "Bulgarian", "bul": "Bulgarian",
	"ca": "Catalan", "cat": "Catalan",
	"cs": "Czech", "ces": "Czech", "cze": "Czech",
	"da": "Danish", "dan": "Danish",
	"de": "German", "deu": "German", "ger": "German",
	"el": "Greek", "ell": "Greek", "gre": "Greek",
	"en": "English", "eng": "English",
	"es": "Spanish", "spa": "Spanish",
	"et": "Estonian", "est": "Estonian",
	"fa": "Persian", "fas": "Persian", "per": "Persian",
	"fi": "Finnish", "fin": "Finnish",
	"fil": "Filipino",
	"fr":  "French", "fra": "French", "fre": "French",
	"he": "Hebrew", "heb": "Hebrew",
	"hi": "Hindi", "hin": "Hindi",
	"hr": "Croatian", "hrv": "Croatian",
	"hu": "Hungarian", "hun": "Hungarian",
	"id": "Indonesian", "ind": "Indonesian",
	"is": "Icelandic", "isl": "Icelandic", "ice": "Icelandic",
	"it": "Italian", "ita": "Italian",
	"ja": "Japanese", "jpn": "Japanese",
	"ko": "Korean", "kor": "Korean",
	"lt": "Lithuanian", "lit": "Lithuanian",
	"lv": "Latvian", "lav": "Latvian",
	"ms": "Malay", "msa": "Malay", "may": "Malay",
	"nb": "Norwegian Bokmål", "nob": "Norwegian Bokmål",
	"nl": "Dutch", "nld": "Dutch", "dut": "Dutch",
	"no": "Norwegian", "nor": "Norwegian",
	"pl": "Polish", "pol": "Polish",
	"pt": "Portuguese", "por": "Portuguese",
	"ro": "Romanian", "ron": "Romanian", "rum": "Romanian",
	"ru": "Russian", "rus": "Russian",
	"sk": "Slovak", "slk": "Slovak", "slo": "Slovak",
	"sl": "Slovenian", "slv": "Slovenian",
	"sr": "Serbian", "srp": "Serbian",
	"sv": "Swedish", "swe": "Swedish",
	"ta": "Tamil", "tam": "Tamil",
	"th": "Thai", "tha": "Thai",
	"tr": "Turkish", "tur": "Turkish",
	"uk": "Ukrainian", "ukr": "Ukrainian",
	"vi": "Vietnamese", "vie": "Vietnamese",
	"yue": "Cantonese",
	"zh":  "Chinese", "zho": "Chinese", "chi": "Chinese",
	"cmn": "Mandarin",
}
