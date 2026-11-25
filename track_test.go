package player

import "testing"

func TestTrackDisplayName(t *testing.T) {
	cases := []struct {
		name string
		tr   Track
		want string
	}{
		{"lang and title", Track{ID: 2, Lang: "jpn", Title: "Commentary"}, "Japanese · Commentary"},
		{"two-letter lang", Track{ID: 1, Lang: "en"}, "English"},
		{"region suffix", Track{ID: 1, Lang: "pt-BR"}, "Portuguese"},
		{"title repeats lang", Track{ID: 1, Lang: "eng", Title: "English"}, "English"},
		{"forced", Track{ID: 3, Lang: "eng", Forced: true}, "English · Forced"},
		{"unknown code", Track{ID: 4, Lang: "xyz"}, "XYZ"},
		{"undetermined", Track{ID: 5, Lang: "und"}, "Track 5"},
		{"nothing known", Track{ID: 7}, "Track 7"},
		{"title only", Track{ID: 1, Title: "Director's Cut"}, "Director's Cut"},
		{
			"external auto-loaded",
			Track{ID: 1, Title: "srt", External: true, ExternalFilename: "/movies/movie.srt"},
			"movie.srt",
		},
		{
			"external with lang",
			Track{ID: 1, Title: "en.srt", Lang: "en", External: true, ExternalFilename: "/movies/movie.en.srt"},
			"English · movie.en.srt",
		},
		{
			"external with real title",
			Track{ID: 1, Title: "Signs & Songs", External: true, ExternalFilename: "/movies/signs.ass"},
			"Signs & Songs",
		},
	}
	for _, tc := range cases {
		if got := tc.tr.DisplayName(); got != tc.want {
			t.Errorf("%s: DisplayName() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestLanguageName(t *testing.T) {
	for code, want := range map[string]string{
		"":      "",
		"und":   "",
		"JPN":   "Japanese",
		"zh_CN": "Chinese",
		"ger":   "German",
		"deu":   "German",
		"tlh":   "TLH",
	} {
		if got := LanguageName(code); got != want {
			t.Errorf("LanguageName(%q) = %q, want %q", code, got, want)
		}
	}
}
