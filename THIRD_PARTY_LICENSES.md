# Third-Party Licenses & LGPL 2.1+ Compliance

The Go source code, native bindings, examples, and build scripts in
`github.com/arixbit/mygo-player` are licensed under the **MIT License** (see
[LICENSE](LICENSE)).

## libmpv (LGPL 2.1 or later)

At runtime, `mygo-player` dynamically loads `libmpv` via `mygo/ffi` (`dlopen` /
`LoadLibrary`) with zero cgo.

The official prebuilt `libmpv` binaries published by this repository's GitHub
Actions workflows are compiled **strictly without GPL components** (`mpv` built
with `-Dgpl=false`; `FFmpeg` built without `--enable-gpl`, `--enable-nonfree`,
or `--enable-version3`), making the distributed `libmpv` shared library licensed
under the **GNU Lesser General Public License v2.1 or later (LGPL-2.1-or-later)**.

### Dynamic Relinking (LGPL 2.1 Section 6)

Because `libmpv` is loaded dynamically at runtime via `mygo/ffi`, end users and
downstream applications can replace the `libmpv` shared library at any time
without recompiling the Go application:

- Set the `MYGO_NATIVE_MPV` environment variable to the path of a compatible
  `libmpv` shared library (`libmpv.dylib` / `libmpv.2.dylib` on macOS;
  `mpv-2.dll` on Windows; `libmpv.so` / `libmpv.so.2` on Linux), or
- Replace the bundled shared library directly:
  - **macOS:** `Contents/Resources/libmpv.dylib` inside a packaged `.app` bundle
  - **Windows:** `mpv-2.dll` next to the application executable
  - **Linux:** `libmpv.so` next to the application executable (or inside its bundled native library directory)

### Included Third-Party Libraries in Prebuilt `libmpv`

When `scripts/build-libmpv-macos.sh`, `scripts/build-libmpv-windows.sh`, or
`scripts/build-libmpv-linux.sh` builds a self-contained `libmpv.dylib`,
`mpv-2.dll`, or `libmpv.so`, it statically links only LGPL-2.1+, MIT, BSD, ISC,
FTL, or Zlib-compatible libraries:

- **mpv** — LGPL-2.1-or-later (when built with `-Dgpl=false`)
- **FFmpeg** (`libavcodec`, `libavformat`, `libavutil`, `libavfilter`, `libswscale`, `libswresample`) — LGPL-2.1-or-later (built without `--enable-gpl`, `--enable-version3`, or `--enable-nonfree`)
- **libplacebo** — LGPL-2.1-or-later
- **libass** — ISC
- **FreeType** — FreeType License (FTL) / GPLv2 (used under FTL)
- **HarfBuzz** — MIT
- **FriBidi** — LGPL-2.1-or-later
- **Little CMS (lcms2)** — MIT
- **dav1d** — BSD-2-Clause
- **libunibreak** — Zlib
- **libiconv** (static builds where needed) — LGPL-2.1-or-later
- **zlib** (static builds where needed) — Zlib

Full corresponding source code versions and exact configure flags used to build
each published `libmpv` release artifact are recorded in
`scripts/build-libmpv-macos.sh`, `scripts/build-libmpv-windows.sh`, and
`scripts/build-libmpv-linux.sh`, and attached to each GitHub Release.
