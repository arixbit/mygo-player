package main

import "github.com/egoist/mygo/ui"

// Icons drawn for this example on a 24×24 grid. ui.Icon paints them in the
// current text color, so they follow the theme like text does.

func svgIcon(body string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
}

var (
	iconPlay   = svgIcon(`<path fill="currentColor" stroke="none" d="M8 5.14v13.72a1 1 0 0 0 1.52.85l11.03-6.86a1 1 0 0 0 0-1.7L9.52 4.29A1 1 0 0 0 8 5.14z"/>`)
	iconPause  = svgIcon(`<rect fill="currentColor" stroke="none" x="6" y="4.5" width="4" height="15" rx="1.2"/><rect fill="currentColor" stroke="none" x="14" y="4.5" width="4" height="15" rx="1.2"/>`)
	iconReplay = svgIcon(`<path d="M3.5 12a8.5 8.5 0 1 0 2.6-6.1L3.5 8.5"/><path d="M3.5 3.5v5h5"/>`)

	iconBack    = svgIcon(`<path fill="currentColor" stroke="none" d="M11 6.5v11a.8.8 0 0 1-1.25.66L2.6 12.66a.8.8 0 0 1 0-1.32l7.15-5.5A.8.8 0 0 1 11 6.5zM21 6.5v11a.8.8 0 0 1-1.25.66l-7.15-5.5a.8.8 0 0 1 0-1.32l7.15-5.5A.8.8 0 0 1 21 6.5z"/>`)
	iconForward = svgIcon(`<path fill="currentColor" stroke="none" d="M3 6.5v11a.8.8 0 0 0 1.25.66l7.15-5.5a.8.8 0 0 0 0-1.32L4.25 5.84A.8.8 0 0 0 3 6.5zM13 6.5v11a.8.8 0 0 0 1.25.66l7.15-5.5a.8.8 0 0 0 0-1.32l-7.15-5.5A.8.8 0 0 0 13 6.5z"/>`)

	speakerPath     = `<path fill="currentColor" stroke="none" d="M4 9.5h3l4.3-3.6a.6.6 0 0 1 1 .46v11.28a.6.6 0 0 1-1 .46L7 14.5H4a1 1 0 0 1-1-1v-3a1 1 0 0 1 1-1z"/>`
	iconVolumeHigh  = svgIcon(speakerPath + `<path d="M15.5 9.5a3.5 3.5 0 0 1 0 5"/><path d="M18.5 6.5a7.5 7.5 0 0 1 0 11"/>`)
	iconVolumeLow   = svgIcon(speakerPath + `<path d="M15.5 9.5a3.5 3.5 0 0 1 0 5"/>`)
	iconVolumeMuted = svgIcon(speakerPath + `<path d="M16 9.5l5 5M21 9.5l-5 5"/>`)

	iconSubtitlesOn  = svgIcon(`<path fill="currentColor" stroke="none" fill-rule="evenodd" d="M6 5h12a3 3 0 0 1 3 3v8a3 3 0 0 1-3 3H6a3 3 0 0 1-3-3V8a3 3 0 0 1 3-3zm1 7.25h4v1.5H7zm6 0h4v1.5h-4zm-6 3h7v1.5H7z"/>`)
	iconSubtitlesOff = svgIcon(`<rect x="3.75" y="5.75" width="16.5" height="12.5" rx="2.5" stroke-width="1.5"/><path stroke-width="1.5" d="M7.5 13h3M13.5 13h3M7.5 16h6"/>`)
	iconAudioTrack   = svgIcon(`<path d="M4 10v4M8 7v10M12 4v16M16 8v8M20 11v2"/>`)

	iconEnterFullScreen = svgIcon(`<path d="M4 9V5a1 1 0 0 1 1-1h4M15 4h4a1 1 0 0 1 1 1v4M20 15v4a1 1 0 0 1-1 1h-4M9 20H5a1 1 0 0 1-1-1v-4"/>`)
	iconExitFullScreen  = svgIcon(`<path d="M9 4v4a1 1 0 0 1-1 1H4M20 9h-4a1 1 0 0 1-1-1V4M15 20v-4a1 1 0 0 1 1-1h4M4 15h4a1 1 0 0 1 1 1v4"/>`)

	iconFilm  = svgIcon(`<rect x="3" y="4" width="18" height="16" rx="3" stroke-width="1.5"/><path stroke-width="1.5" d="M7.5 4v16M16.5 4v16M3 9h4.5M3 15h4.5M16.5 9H21M16.5 15H21"/>`)
	iconAlert = svgIcon(`<circle cx="12" cy="12" r="9"/><path d="M12 7.5v5.5M12 16.5v.01"/>`)
	iconMusic = svgIcon(`<path d="M9 18V5.5l11-2V16"/><circle cx="6.5" cy="18" r="2.5"/><circle cx="17.5" cy="16" r="2.5"/>`)
	iconMore  = svgIcon(`<circle fill="currentColor" stroke="none" cx="5.5" cy="12" r="1.8"/><circle fill="currentColor" stroke="none" cx="12" cy="12" r="1.8"/><circle fill="currentColor" stroke="none" cx="18.5" cy="12" r="1.8"/>`)
	iconDrop  = svgIcon(`<path d="M12 4v11M7.5 10.5 12 15l4.5-4.5"/><path d="M4 15v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3"/>`)
)
