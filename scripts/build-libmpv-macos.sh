#!/usr/bin/env bash
# Builds a self-contained, strictly LGPL-2.1-or-later libmpv.dylib for macOS
# (arm64 or x86_64) with zero non-system dynamic library dependencies.
#
# Usage:
#   ./scripts/build-libmpv-macos.sh <arm64|x86_64> <output-dylib-path>
#
# License guarantee:
#   - FFmpeg is configured WITHOUT --enable-gpl, --enable-version3, or --enable-nonfree.
#   - mpv is configured with -Dgpl=false.
#   - Only LGPL-2.1+, MIT, BSD, ISC, FTL, or Zlib static dependencies are linked.
#   - Final verification asserts otool -L only references macOS system libs/frameworks.
set -euo pipefail

ARCH="${1:-$(uname -m)}"
OUT_DYLIB="${2:-dist/libmpv-darwin-${ARCH}.dylib}"
MACOS_MIN="${MACOSX_DEPLOYMENT_TARGET:-12.0}"

# Pinned upstream release versions (strictly LGPL-2.1+ / permissive).
FRIBIDI_VER="1.0.16"
FREETYPE_VER="2.13.3"
HARFBUZZ_VER="10.4.0"
LIBUNIBREAK_VER="6.1"
LIBASS_VER="0.17.3"
DAV1D_VER="1.5.1"
LCMS2_VER="2.17"
LIBPLACEBO_VER="v7.349.0"
FFMPEG_VER="7.1.1"
MPV_VER="${MPV_VERSION:-0.40.0}"

case "$ARCH" in
  arm64|aarch64)
    ARCH="arm64"
    FFMPEG_ARCH="arm64"
    ;;
  x86_64|amd64)
    ARCH="x86_64"
    FFMPEG_ARCH="x86_64"
    ;;
  *)
    echo "Unsupported macOS architecture: $ARCH (want arm64 or x86_64)" >&2
    exit 1
    ;;
esac

WORK_DIR="${WORK_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/mygo-libmpv-build.XXXXXX")}"
SRC_DIR="$WORK_DIR/src"
BUILD_DIR="$WORK_DIR/build"
PREFIX="$WORK_DIR/prefix"
JOBS="$(sysctl -n hw.ncpu)"

mkdir -p "$SRC_DIR" "$BUILD_DIR" "$PREFIX" "$(dirname "$OUT_DYLIB")"

export MACOSX_DEPLOYMENT_TARGET="$MACOS_MIN"
export CC="clang"
export CXX="clang++"
export CFLAGS="-arch $ARCH -mmacosx-version-min=$MACOS_MIN -O2 -fPIC -I$PREFIX/include"
export CXXFLAGS="$CFLAGS"
export OBJCFLAGS="$CFLAGS"
export LDFLAGS="-arch $ARCH -mmacosx-version-min=$MACOS_MIN -L$PREFIX/lib"
# Strictly isolate pkg-config from Homebrew /opt/homebrew and /usr/local so no
# accidental dynamic dependencies leak into libmpv.dylib.
export PKG_CONFIG_LIBDIR="$PREFIX/lib/pkgconfig"
export PKG_CONFIG_PATH=""

echo "==> Building strictly LGPL 2.1+ self-contained libmpv $MPV_VER for macOS/$ARCH (min macOS $MACOS_MIN)"
echo "    Work dir: $WORK_DIR"
echo "    Output:   $OUT_DYLIB"

fetch_tar() {
  local url="$1"
  local dest="$2"
  if [[ ! -d "$dest" ]]; then
    mkdir -p "$dest"
    echo "==> Downloading $url"
    curl -fL --retry 3 --retry-delay 2 "$url" | tar -x --strip-components=1 -C "$dest"
  fi
}

# 1. FriBidi (LGPL-2.1+)
fetch_tar "https://github.com/fribidi/fribidi/releases/download/v${FRIBIDI_VER}/fribidi-${FRIBIDI_VER}.tar.xz" "$SRC_DIR/fribidi"
meson setup "$BUILD_DIR/fribidi" "$SRC_DIR/fribidi" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release --default-library=static \
  -Ddocs=false -Dtests=false -Dbin=false
ninja -C "$BUILD_DIR/fribidi" -j"$JOBS" install

# 2. FreeType (FTL)
fetch_tar "https://download.savannah.gnu.org/releases/freetype/freetype-${FREETYPE_VER}.tar.xz" "$SRC_DIR/freetype"
meson setup "$BUILD_DIR/freetype" "$SRC_DIR/freetype" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release --default-library=static \
  -Dharfbuzz=disabled -Dpng=disabled -Dbrotli=disabled -Dbzip2=disabled -Dzlib=system -Dtests=disabled
ninja -C "$BUILD_DIR/freetype" -j"$JOBS" install

# 3. HarfBuzz (MIT)
fetch_tar "https://github.com/harfbuzz/harfbuzz/releases/download/${HARFBUZZ_VER}/harfbuzz-${HARFBUZZ_VER}.tar.xz" "$SRC_DIR/harfbuzz"
meson setup "$BUILD_DIR/harfbuzz" "$SRC_DIR/harfbuzz" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release --default-library=static \
  -Dfreetype=enabled -Dcoretext=enabled \
  -Dglib=disabled -Dgobject=disabled -Dcairo=disabled -Dicu=disabled \
  -Dgraphite=disabled -Dgraphite2=disabled -Dtests=disabled -Ddocs=disabled -Dutilities=disabled
ninja -C "$BUILD_DIR/harfbuzz" -j"$JOBS" install

# 4. libunibreak (Zlib)
fetch_tar "https://github.com/adah1972/libunibreak/releases/download/libunibreak_${LIBUNIBREAK_VER//./_}/libunibreak-${LIBUNIBREAK_VER}.tar.gz" "$SRC_DIR/libunibreak"
(
  cd "$SRC_DIR/libunibreak"
  ./configure --prefix="$PREFIX" --enable-static --disable-shared --disable-dependency-tracking
  make -j"$JOBS" install
)

# 5. libass (ISC) — uses macOS CoreText natively for system font lookup
fetch_tar "https://github.com/libass/libass/releases/download/${LIBASS_VER}/libass-${LIBASS_VER}.tar.xz" "$SRC_DIR/libass"
(
  cd "$SRC_DIR/libass"
  ./configure --prefix="$PREFIX" --enable-static --disable-shared \
    --enable-coretext --disable-fontconfig --disable-libpng --disable-asm
  make -j"$JOBS" install
)

# 6. dav1d (BSD-2-Clause) — fast AV1 decoding
fetch_tar "https://code.videolan.org/videolan/dav1d/-/archive/${DAV1D_VER}/dav1d-${DAV1D_VER}.tar.bz2" "$SRC_DIR/dav1d"
meson setup "$BUILD_DIR/dav1d" "$SRC_DIR/dav1d" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release --default-library=static \
  -Denable_tools=false -Denable_tests=false -Denable_examples=false
ninja -C "$BUILD_DIR/dav1d" -j"$JOBS" install

# 7. Little CMS 2 (MIT)
fetch_tar "https://github.com/mm2/Little-CMS/releases/download/lcms${LCMS2_VER}/lcms2-${LCMS2_VER}.tar.gz" "$SRC_DIR/lcms2"
meson setup "$BUILD_DIR/lcms2" "$SRC_DIR/lcms2" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release --default-library=static \
  -Djpeg=disabled -Dtiff=disabled -Dutils=false -Dtests=disabled
ninja -C "$BUILD_DIR/lcms2" -j"$JOBS" install

# 8. libplacebo (LGPL-2.1+) — clone with submodules (fast_float, glad, jinja, markupsafe)
if [[ ! -d "$SRC_DIR/libplacebo" ]]; then
  git clone --depth 1 --branch "$LIBPLACEBO_VER" --recurse-submodules --shallow-submodules \
    https://code.videolan.org/videolan/libplacebo.git "$SRC_DIR/libplacebo"
fi
meson setup "$BUILD_DIR/libplacebo" "$SRC_DIR/libplacebo" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release --default-library=static \
  -Dopengl=enabled -Dvulkan=disabled -Dd3d11=disabled \
  -Dshaderc=disabled -Dglslang=disabled -Dlcms=enabled \
  -Ddemos=false -Dtests=false -Dbench=false -Dfuzz=false \
  -Dunwind=disabled -Dxxhash=disabled -Dlibdovi=disabled
ninja -C "$BUILD_DIR/libplacebo" -j"$JOBS" install

# 9. FFmpeg (strictly LGPL-2.1+: NO --enable-gpl, NO --enable-version3, NO --enable-nonfree)
fetch_tar "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VER}.tar.xz" "$SRC_DIR/ffmpeg"
(
  mkdir -p "$BUILD_DIR/ffmpeg"
  cd "$BUILD_DIR/ffmpeg"
  "$SRC_DIR/ffmpeg/configure" \
    --prefix="$PREFIX" \
    --arch="$FFMPEG_ARCH" \
    --target-os=darwin \
    --cc="$CC" \
    --cxx="$CXX" \
    --extra-cflags="$CFLAGS" \
    --extra-ldflags="$LDFLAGS" \
    --pkg-config=pkg-config \
    --pkg-config-flags="--static" \
    --enable-static \
    --disable-shared \
    --enable-pic \
    --enable-pthreads \
    --disable-programs \
    --disable-doc \
    --disable-debug \
    --disable-autodetect \
    --enable-zlib \
    --enable-bzlib \
    --enable-iconv \
    --enable-videotoolbox \
    --enable-audiotoolbox \
    --enable-securetransport \
    --enable-libdav1d \
    --enable-network
  make -j"$JOBS" install
)

# 10. mpv / libmpv (strictly LGPL-2.1+: -Dgpl=false)
# Note: Do not pass --prefer-static to mpv's meson setup because macOS SDK only
# ships libiconv.tbd / libz.tbd (no static .a). All third-party dependencies in
# $PREFIX/lib were built static-only; wrap pkg-config with --static so mpv pulls
# every Libs.private entry (Apple frameworks, dav1d, harfbuzz, freetype, etc.)
# while still resolving macOS system iconv/zlib cleanly.
mkdir -p "$WORK_DIR/bin"
cat > "$WORK_DIR/bin/pkg-config-static" <<'EOF'
#!/usr/bin/env bash
for arg in "$@"; do
  case "$arg" in
    --libs|--libs-only-l|--libs-only-L|--libs-only-other)
      exec pkg-config --static "$@"
      ;;
  esac
done
exec pkg-config "$@"
EOF
chmod +x "$WORK_DIR/bin/pkg-config-static"

cat > "$BUILD_DIR/mpv-native.ini" <<EOF
[binaries]
pkg-config = '$WORK_DIR/bin/pkg-config-static'
EOF

fetch_tar "https://github.com/mpv-player/mpv/archive/refs/tags/v${MPV_VER}.tar.gz" "$SRC_DIR/mpv"
meson setup "$BUILD_DIR/mpv" "$SRC_DIR/mpv" \
  --native-file "$BUILD_DIR/mpv-native.ini" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release \
  -Dgpl=false \
  -Dlibmpv=true \
  -Dcplayer=false \
  -Dbuild-date=false \
  -Dtests=false \
  -Dgl=enabled \
  -Dgl-cocoa=enabled \
  -Dplain-gl=enabled \
  -Dvideotoolbox-gl=enabled \
  -Dvideotoolbox-pl=disabled \
  -Dcoreaudio=enabled \
  -Dcocoa=enabled \
  -Diconv=enabled \
  -Dzlib=enabled \
  -Dcplugins=disabled \
  -Dlibavdevice=disabled \
  -Dswift-build=disabled \
  -Dmacos-cocoa-cb=disabled \
  -Dmacos-media-player=disabled \
  -Dmacos-touchbar=disabled \
  -Dvulkan=disabled \
  -Dlua=disabled \
  -Djavascript=disabled \
  -Duchardet=disabled \
  -Drubberband=disabled \
  -Dzimg=disabled \
  -Dvapoursynth=disabled \
  -Dlibarchive=disabled \
  -Dlibbluray=disabled \
  -Ddvdnav=disabled \
  -Dcdda=disabled \
  -Djpeg=disabled \
  -Dlcms2=enabled \
  -Dhtml-build=disabled \
  -Dmanpage-build=disabled \
  -Dpdf-build=disabled \
  -Dc_link_args="['-Wl,-dead_strip','-lc++']"
ninja -C "$BUILD_DIR/mpv" -j"$JOBS" install

BUILT_DYLIB="$(find "$PREFIX/lib" -maxdepth 1 -name 'libmpv*.dylib' -type f | head -n 1)"
if [[ -z "$BUILT_DYLIB" ]]; then
  echo "ERROR: libmpv dylib not found in $PREFIX/lib" >&2
  exit 1
fi

cp "$BUILT_DYLIB" "$OUT_DYLIB"
chmod 755 "$OUT_DYLIB"
install_name_tool -id "@rpath/libmpv.dylib" "$OUT_DYLIB"
strip -x "$OUT_DYLIB"
codesign --force --sign - "$OUT_DYLIB"

echo "==> Verifying $OUT_DYLIB"
file "$OUT_DYLIB"
if ! file "$OUT_DYLIB" | grep -q "$ARCH"; then
  echo "ERROR: $OUT_DYLIB architecture does not match $ARCH" >&2
  exit 1
fi

echo "==> Checking dynamic dependencies (must be macOS system libraries/frameworks only):"
otool -L "$OUT_DYLIB"
BAD_DEPS="$(otool -L "$OUT_DYLIB" | tail -n +2 | awk '{print $1}' | grep -vE '^(/usr/lib/|/System/Library/Frameworks/|@rpath/libmpv\.dylib$)' || true)"
if [[ -n "$BAD_DEPS" ]]; then
  echo "ERROR: non-system dynamic dependencies detected in $OUT_DYLIB:" >&2
  echo "$BAD_DEPS" >&2
  exit 1
fi

echo "==> Verifying exported libmpv symbols:"
for sym in _mpv_create _mpv_initialize _mpv_command _mpv_render_context_create _mpv_render_context_render _mpv_terminate_destroy; do
  if ! nm -gU "$OUT_DYLIB" | grep -q " T ${sym}$"; then
    echo "ERROR: missing required exported symbol ${sym}" >&2
    exit 1
  fi
done

echo "==> SHA-256:"
shasum -a 256 "$OUT_DYLIB"
echo "==> Done: $OUT_DYLIB"
