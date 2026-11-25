package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// The player's motion language. Every state change in the UI uses one of
// these tokens, so the same kind of change always moves the same way:
//
//   - Things the user does over and over (hover, press, icon swaps, values
//     changing) are the fastest and lightest.
//   - Chrome (the control bar, the cursor, subtitles making room) comes in
//     quickly and leaves slowly, so it never feels like it was snatched away.
//   - Overlays (the welcome card, loading, errors, drop target, toasts) fade
//     and scale a few percent from where they sit, explaining that they
//     belong to the stage rather than flying in from elsewhere.
//
// Positions and scales snap while the desktop asks for less motion (MyGo
// does this for every Animation/Transition); fades and colors still ease,
// which is what Apple's guidelines recommend. Values that only fade are
// marked NonMotion so they keep easing too.
var (
	// easeOutSoft decelerates like UIKit's default spring without bounce:
	// quick to respond, gentle to settle.
	easeOutSoft = ui.CubicBezier(0.2, 0.8, 0.2, 1)
	// easeInOutSoft is used for things leaving on their own (not in
	// response to a press), which should not draw attention.
	easeInOutSoft = ui.CubicBezier(0.4, 0, 0.2, 1)

	// motionHover tints a control under the pointer.
	motionHover = ui.Animation{Duration: 120 * time.Millisecond, Ease: ui.EaseOut, Oklab: true, NonMotion: true}
	// motionPress squeezes a control while pressed and springs it back; a
	// spring keeps its velocity when clicked again mid-flight.
	motionPress = ui.Animation{Spring: ui.SpringOptions{Duration: 220 * time.Millisecond, Bounce: 0.15}}
	// motionTrack grows the seek and volume tracks under the pointer.
	motionTrack = ui.Animation{Spring: ui.SpringOptions{Duration: 200 * time.Millisecond}}

	// motionChromeIn and motionChromeOut show and hide the control bar.
	motionChromeIn  = ui.Animation{Duration: 200 * time.Millisecond, Ease: easeOutSoft}
	motionChromeOut = ui.Animation{Duration: 360 * time.Millisecond, Ease: easeInOutSoft}

	// motionStage cross-fades the stage between the welcome screen and the
	// video, and fades the picture in once its first frame is ready.
	motionStage = ui.Animation{Duration: 280 * time.Millisecond, Ease: easeOutSoft, NonMotion: true}

	// contentSwap swaps an icon or a label in place (play ⇄ pause, volume
	// levels, subtitles on/off): the old one shrinks away as the new grows.
	contentSwap = ui.ContentTransition{Animation: &ui.Animation{Spring: ui.SpringOptions{Duration: 240 * time.Millisecond}}}
)

// numericSwap rolls the changed digits of a value the user set (playback
// speed), down when it decreased. The playback clock deliberately does not
// roll: digits ticking every second would pull the eye off the picture.
func numericSwap(down bool) ui.ContentTransition {
	return ui.ContentTransition{
		Numeric:   true,
		Down:      down,
		Animation: &ui.Animation{Spring: ui.SpringOptions{Duration: 240 * time.Millisecond}},
	}
}

var (
	// overlayTransition brings stage overlays (welcome card, error card,
	// drop target) in and out where they sit.
	overlayTransition = ui.ElementTransition{
		Duration:     220 * time.Millisecond,
		Ease:         easeOutSoft,
		ExitDuration: 160 * time.Millisecond,
		ExitEase:     ui.EaseIn,
		Enter:        &ui.Motion{Scale: 0.96},
		Exit:         &ui.Motion{Scale: 0.98},
	}

	// loadingTransition waits a moment before showing a spinner, so quick
	// loads and short stalls never flash one, and leaves at once.
	loadingTransition = ui.ElementTransition{
		Animation:    &ui.Animation{Duration: 220 * time.Millisecond, Ease: easeOutSoft, Delay: 300 * time.Millisecond},
		ExitDuration: 160 * time.Millisecond,
		ExitEase:     ui.EaseIn,
		ExitDelay:    -1,
		Enter:        &ui.Motion{Scale: 0.9},
		Exit:         &ui.Motion{Scale: 0.96},
	}

	// popoverTransition opens menus from the button they belong to.
	popoverTransition = ui.ElementTransition{
		Duration:     180 * time.Millisecond,
		Ease:         easeOutSoft,
		ExitDuration: 120 * time.Millisecond,
		ExitEase:     ui.EaseIn,
		Enter:        &ui.Motion{Scale: 0.95, Y: 6, Flip: true},
		Exit:         &ui.Motion{Scale: 0.97, Y: 3, Flip: true},
	}

	// toastTransition drops a toast in from the top edge it lives at.
	toastTransition = ui.ElementTransition{
		Animation:    &ui.Animation{Spring: ui.SpringOptions{Duration: 320 * time.Millisecond, Bounce: 0.05}},
		ExitDuration: 160 * time.Millisecond,
		ExitEase:     ui.EaseIn,
		Enter:        &ui.Motion{Y: -12, Scale: 0.98},
		Exit:         &ui.Motion{Y: -6, Scale: 0.98},
	}

	// tipTransition shows the time preview above the seek bar.
	tipTransition = ui.ElementTransition{
		Duration:     120 * time.Millisecond,
		Ease:         ui.EaseOut,
		ExitDuration: 100 * time.Millisecond,
		Enter:        &ui.Motion{Y: 4, Scale: 0.96},
		Exit:         &ui.Motion{Y: 2, Scale: 0.98},
		// Only colors ease once shown: the tip follows the pointer 1:1.
		Colors: true,
	}
)
