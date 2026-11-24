#!/usr/bin/env bash
# Builds a self-contained, strictly LGPL-2.1-or-later libmpv.so for Linux
# (amd64/x86_64 or arm64/aarch64) with only standard Linux system library
# dynamic dependencies.
#
# Usage:
#   ./scripts/build-libmpv-linux.sh <amd64|x86_64|arm64|aarch64> <output-so-path>
#
# License guarantee:
#   - FFmpeg is configured WITHOUT --enable-gpl, --enable-version3, or --enable-nonfree.
#   - mpv is configured with -Dgpl=false.
#   - Only LGPL-2.1+, MIT, BSD, ISC, FTL, or Zlib static dependencies are linked.
#   - Final verification asserts readelf -d NEEDED only references allowlisted
#     Linux system libraries.
set -euo pipefail

ARCH_ARG="${1:-$(uname -m)}"

# Pinned upstream release versions (strictly LGPL-2.1+ / permissive).
ZLIB_VER="1.3.1"
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

case "$ARCH_ARG" in
  x86_64|amd64)
    ARCH="x86_64"
    GOARCH="amd64"
    FFMPEG_ARCH="x86_64"
    ELF_MACHINE="Advanced Micro Devices X86-64"
    ;;
  arm64|aarch64)
    ARCH="aarch64"
    GOARCH="arm64"
    FFMPEG_ARCH="aarch64"
    ELF_MACHINE="AArch64"
    ;;
  *)
    echo "Unsupported Linux architecture: $ARCH_ARG (want amd64/x86_64 or arm64/aarch64)" >&2
    exit 1
    ;;
esac

OUT_SO_ARG="${2:-dist/libmpv-linux-${GOARCH}.so}"
mkdir -p "$(dirname "$OUT_SO_ARG")"
OUT_SO="$(cd "$(dirname "$OUT_SO_ARG")" && pwd)/$(basename "$OUT_SO_ARG")"

WORK_DIR="${WORK_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/mygo-libmpv-linux.XXXXXX")}"
SRC_DIR="$WORK_DIR/src"
BUILD_DIR="$WORK_DIR/build"
PREFIX="$WORK_DIR/prefix"
SYS_PC_DIR="$WORK_DIR/sys-pkgconfig"
JOBS="$(nproc)"

mkdir -p "$SRC_DIR" "$BUILD_DIR" "$PREFIX/include" "$PREFIX/lib/pkgconfig" "$SYS_PC_DIR"

# Export minimal public-only .pc stubs for the exact allowlisted Linux system
# shared libraries BEFORE isolating PKG_CONFIG_LIBDIR. Dropping Libs.private and
# Requires.private prevents pkg-config --static from dragging transitive system
# private dependencies (nettle, hogweed, p11-kit, brotli, zstd, dbus, etc.) into
# libmpv.so's direct DT_NEEDED table, while keeping every other host .pc invisible.
for pkg in fontconfig gnutls alsa libpulse libva libva-drm; do
  if ! pkg-config --exists "$pkg"; then
    echo "ERROR: required Linux system pkg-config package '$pkg' not found" >&2
    exit 1
  fi
  pkg_ver="$(pkg-config --modversion "$pkg")"
  pkg_libs="$(pkg-config --libs "$pkg")"
  pkg_cflags="$(pkg-config --cflags "$pkg")"
  cat > "$SYS_PC_DIR/${pkg}.pc" <<EOF
Name: ${pkg}
Description: Host system shared library (${pkg})
Version: ${pkg_ver}
Libs: ${pkg_libs}
Cflags: ${pkg_cflags}
EOF
done

export CC="gcc"
export CXX="g++"
export CFLAGS="-O2 -fPIC -I$PREFIX/include"
export CXXFLAGS="$CFLAGS"
export LDFLAGS="-L$PREFIX/lib"
export PKG_CONFIG_LIBDIR="$PREFIX/lib/pkgconfig:$SYS_PC_DIR"
export PKG_CONFIG_PATH=""

echo "==> Building strictly LGPL 2.1+ self-contained libmpv $MPV_VER for linux/$ARCH ($GOARCH)"
echo "    Work dir: $WORK_DIR"
echo "    Output:   $OUT_SO"

fetch_tar() {
  local url="$1"
  local dest="$2"
  if [[ ! -d "$dest" ]]; then
    mkdir -p "$dest"
    local archive="$WORK_DIR/$(basename "$url")"
    echo "==> Downloading $url"
    curl -fL --retry 3 --retry-delay 2 -o "$archive" "$url"
    tar -xf "$archive" --strip-components=1 -C "$dest"
    rm -f "$archive"
  fi
}

# 1. zlib (Zlib) — static libz.a
fetch_tar "https://github.com/madler/zlib/releases/download/v${ZLIB_VER}/zlib-${ZLIB_VER}.tar.gz" "$SRC_DIR/zlib"
(
  cd "$SRC_DIR/zlib"
  ./configure --prefix="$PREFIX" --static
  make -j"$JOBS" install
)
pkg-config --cflags --libs zlib

# 2. FriBidi (LGPL-2.1+)
fetch_tar "https://github.com/fribidi/fribidi/releases/download/v${FRIBIDI_VER}/fribidi-${FRIBIDI_VER}.tar.xz" "$SRC_DIR/fribidi"
meson setup "$BUILD_DIR/fribidi" "$SRC_DIR/fribidi" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release --default-library=static \
  -Ddocs=false -Dtests=false -Dbin=false
ninja -C "$BUILD_DIR/fribidi" -j"$JOBS" install

# 3. FreeType (FTL)
fetch_tar "https://download.savannah.gnu.org/releases/freetype/freetype-${FREETYPE_VER}.tar.xz" "$SRC_DIR/freetype"
meson setup "$BUILD_DIR/freetype" "$SRC_DIR/freetype" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release --default-library=static \
  -Dharfbuzz=disabled -Dpng=disabled -Dbrotli=disabled -Dbzip2=disabled -Dzlib=external -Dtests=disabled
ninja -C "$BUILD_DIR/freetype" -j"$JOBS" install

# 4. HarfBuzz (MIT)
fetch_tar "https://github.com/harfbuzz/harfbuzz/releases/download/${HARFBUZZ_VER}/harfbuzz-${HARFBUZZ_VER}.tar.xz" "$SRC_DIR/harfbuzz"
meson setup "$BUILD_DIR/harfbuzz" "$SRC_DIR/harfbuzz" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release --default-library=static \
  -Dfreetype=enabled \
  -Dglib=disabled -Dgobject=disabled -Dcairo=disabled -Dicu=disabled \
  -Dgraphite=disabled -Dgraphite2=disabled -Dtests=disabled -Ddocs=disabled -Dutilities=disabled
ninja -C "$BUILD_DIR/harfbuzz" -j"$JOBS" install

# 5. libunibreak (Zlib)
fetch_tar "https://github.com/adah1972/libunibreak/releases/download/libunibreak_${LIBUNIBREAK_VER//./_}/libunibreak-${LIBUNIBREAK_VER}.tar.gz" "$SRC_DIR/libunibreak"
(
  cd "$SRC_DIR/libunibreak"
  ./configure --prefix="$PREFIX" --enable-static --disable-shared --disable-dependency-tracking
  make -j"$JOBS" install
)

# 6. libass (ISC) — uses system Fontconfig for Linux font discovery
fetch_tar "https://github.com/libass/libass/releases/download/${LIBASS_VER}/libass-${LIBASS_VER}.tar.xz" "$SRC_DIR/libass"
(
  cd "$SRC_DIR/libass"
  ./configure --prefix="$PREFIX" --enable-static --disable-shared \
    --enable-fontconfig --disable-libpng --disable-asm
  make -j"$JOBS" install
)

# 7. dav1d (BSD-2-Clause) — fast AV1 decoding
fetch_tar "https://code.videolan.org/videolan/dav1d/-/archive/${DAV1D_VER}/dav1d-${DAV1D_VER}.tar.bz2" "$SRC_DIR/dav1d"
meson setup "$BUILD_DIR/dav1d" "$SRC_DIR/dav1d" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release --default-library=static \
  -Denable_tools=false -Denable_tests=false -Denable_examples=false
ninja -C "$BUILD_DIR/dav1d" -j"$JOBS" install

# 8. Little CMS 2 (MIT)
fetch_tar "https://github.com/mm2/Little-CMS/releases/download/lcms${LCMS2_VER}/lcms2-${LCMS2_VER}.tar.gz" "$SRC_DIR/lcms2"
meson setup "$BUILD_DIR/lcms2" "$SRC_DIR/lcms2" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release --default-library=static \
  -Djpeg=disabled -Dtiff=disabled -Dutils=false -Dtests=disabled
ninja -C "$BUILD_DIR/lcms2" -j"$JOBS" install

# 9. libplacebo (LGPL-2.1+) — clone with submodules (fast_float, glad, jinja, markupsafe)
if [[ ! -d "$SRC_DIR/libplacebo" ]]; then
  git clone --depth 1 --branch "$LIBPLACEBO_VER" --recurse-submodules --shallow-submodules \
    https://code.videolan.org/videolan/libplacebo.git "$SRC_DIR/libplacebo"
fi
meson setup "$BUILD_DIR/libplacebo" "$SRC_DIR/libplacebo" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release --default-library=static \
  -Dopengl=disabled -Dvulkan=disabled -Dd3d11=disabled \
  -Dshaderc=disabled -Dglslang=disabled -Dlcms=enabled \
  -Ddemos=false -Dtests=false -Dbench=false -Dfuzz=false \
  -Dunwind=disabled -Dxxhash=disabled -Dlibdovi=disabled
ninja -C "$BUILD_DIR/libplacebo" -j"$JOBS" install
# Ensure libplacebo.pc declares -lstdc++ in Libs.private so pkg-config-static
# links libstdc++.a right after libplacebo.a (for fast_float / std::from_chars).
if [[ -f "$PREFIX/lib/pkgconfig/libplacebo.pc" ]] && ! grep -q -- '-lstdc++' "$PREFIX/lib/pkgconfig/libplacebo.pc"; then
  if grep -q '^Libs\.private:' "$PREFIX/lib/pkgconfig/libplacebo.pc"; then
    sed -i 's/^Libs\.private:.*/& -lstdc++/' "$PREFIX/lib/pkgconfig/libplacebo.pc"
  else
    printf '\nLibs.private: -lstdc++\n' >> "$PREFIX/lib/pkgconfig/libplacebo.pc"
  fi
fi

# 10. FFmpeg (strictly LGPL-2.1+: NO --enable-gpl, NO --enable-version3, NO --enable-nonfree)
# GnuTLS is LGPL-2.1+ and enables HTTPS without GPLv3/nonfree; --enable-vaapi
# enables libva + libva-drm so mpv's hwdec=auto-copy / vaapi-copy works via
# FFmpeg's av_hwdevice_ctx_create(AV_HWDEVICE_TYPE_VAAPI).
fetch_tar "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VER}.tar.xz" "$SRC_DIR/ffmpeg"
(
  mkdir -p "$BUILD_DIR/ffmpeg"
  cd "$BUILD_DIR/ffmpeg"
  "$SRC_DIR/ffmpeg/configure" \
    --prefix="$PREFIX" \
    --arch="$FFMPEG_ARCH" \
    --target-os=linux \
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
    --enable-iconv \
    --enable-vaapi \
    --enable-gnutls \
    --enable-libdav1d \
    --enable-network
  make -j"$JOBS" install
)

# 11. mpv / libmpv (strictly LGPL-2.1+: -Dgpl=false)
# All custom third-party libraries in $PREFIX/lib are static-only (.a). Wrap
# pkg-config with --static so mpv pulls Libs.private from $PREFIX/*.pc while
# our stripped $SYS_PC_DIR/*.pc stubs emit only public dynamic flags for system
# libs (alsa, pulse, fontconfig, gnutls, libva, libva-drm). Rewrite -lstdc++
# (needed by static harfbuzz/libplacebo) into a single atomic -Wl flag so gcc
# links libstdc++.a statically instead of dynamically depending on libstdc++.so.6.
mkdir -p "$WORK_DIR/bin"
cat > "$WORK_DIR/bin/pkg-config-static" <<'EOF'
#!/usr/bin/env bash
for arg in "$@"; do
  case "$arg" in
    --libs|--libs-only-l|--libs-only-L|--libs-only-other)
      out="$(pkg-config --static "$@")" || exit $?
      printf '%s\n' "$out" | sed -E 's/(^|[[:space:]])-lstdc\+\+([[:space:]]|$)/\1-Wl,-Bstatic,-lstdc++,-Bdynamic -lm\2/g'
      exit 0
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

# Note on VAAPI in mpv v0.40.0:
# mpv's meson.build requires -Dvaapi=enabled to have at least one display backend
# (vaapi-x11 -> requires GPL+X11, vaapi-wayland -> requires Wayland, or
# vaapi-drm -> requires libdrm + libdisplay-info). Because Linux rendering uses
# MPV_RENDER_API_TYPE_SW with hwdec=auto-copy, we keep mpv's windowing/VO VAAPI
# interop disabled so libmpv.so never depends on libX11, libwayland, libEGL, or
# libdisplay-info; mpv's vd_lavc.c still drives vaapi-copy hardware decoding
# directly through FFmpeg's --enable-vaapi (libva + libva-drm).
fetch_tar "https://github.com/mpv-player/mpv/archive/refs/tags/v${MPV_VER}.tar.gz" "$SRC_DIR/mpv"
meson setup "$BUILD_DIR/mpv" "$SRC_DIR/mpv" \
  --native-file "$BUILD_DIR/mpv-native.ini" \
  --prefix="$PREFIX" --libdir=lib --buildtype=release \
  --default-library=shared \
  -Dgpl=false \
  -Dlibmpv=true \
  -Dcplayer=false \
  -Dbuild-date=false \
  -Dtests=false \
  -Dalsa=enabled \
  -Dpulse=enabled \
  -Dpipewire=disabled \
  -Djack=disabled \
  -Dopenal=disabled \
  -Doss-audio=disabled \
  -Dsndio=disabled \
  -Dsdl2=disabled \
  -Dsdl2-audio=disabled \
  -Dsdl2-video=disabled \
  -Dsdl2-gamepad=disabled \
  -Dgl=disabled \
  -Dplain-gl=disabled \
  -Degl=disabled \
  -Degl-drm=disabled \
  -Degl-wayland=disabled \
  -Degl-x11=disabled \
  -Dgl-x11=disabled \
  -Dvulkan=disabled \
  -Dwayland=disabled \
  -Ddmabuf-wayland=disabled \
  -Dx11=disabled \
  -Dxv=disabled \
  -Ddrm=disabled \
  -Dgbm=disabled \
  -Dvaapi=disabled \
  -Dvaapi-drm=disabled \
  -Dvaapi-wayland=disabled \
  -Dvaapi-x11=disabled \
  -Dvaapi-win32=disabled \
  -Dvdpau=disabled \
  -Dvdpau-gl-x11=disabled \
  -Dcuda-hwaccel=disabled \
  -Dcuda-interop=disabled \
  -Dcaca=disabled \
  -Dsixel=disabled \
  -Dshaderc=disabled \
  -Dspirv-cross=disabled \
  -Diconv=enabled \
  -Dzlib=enabled \
  -Dlcms2=enabled \
  -Dcplugins=disabled \
  -Dlibavdevice=disabled \
  -Ddvbin=disabled \
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
  -Dhtml-build=disabled \
  -Dmanpage-build=disabled \
  -Dpdf-build=disabled \
  -Dc_link_args="['-L${PREFIX}/lib','-static-libgcc','-static-libstdc++','-Wl,--as-needed','-Wl,-z,defs','-Wl,--exclude-libs,ALL','-Wl,-soname,libmpv.so']"
ninja -C "$BUILD_DIR/mpv" -j"$JOBS" install

BUILT_SO="$(find "$PREFIX/lib" -maxdepth 1 -name 'libmpv.so*' -type f | head -n 1)"
if [[ -z "$BUILT_SO" ]]; then
  echo "ERROR: libmpv shared object not found in $PREFIX/lib" >&2
  exit 1
fi

cp "$BUILT_SO" "$OUT_SO"
chmod 755 "$OUT_SO"
strip --strip-unneeded "$OUT_SO"

echo "==> Verifying $OUT_SO"
file "$OUT_SO"
READELF_H="$(readelf -h "$OUT_SO")"
echo "$READELF_H"
if ! grep -Fq "$ELF_MACHINE" <<< "$READELF_H"; then
  echo "ERROR: $OUT_SO ELF machine does not match $ELF_MACHINE" >&2
  exit 1
fi

echo "==> Checking ELF SONAME and dynamic dependencies via readelf -d:"
READELF_D="$(readelf -d "$OUT_SO")"
echo "$READELF_D"
if ! grep -Fq "Library soname: [libmpv.so]" <<< "$READELF_D"; then
  echo "ERROR: expected SONAME [libmpv.so] in $OUT_SO" >&2
  exit 1
fi

NEEDED_LIBS="$(awk -F'[][]' '/\(NEEDED\)/{print $2}' <<< "$READELF_D" | sort -u)"
echo "==> NEEDED libraries:"
echo "$NEEDED_LIBS"

# Strict allowlist of standard Linux runtime and enabled system libraries.
LINUX_SYS_LIB_RE='^(libc|libm|libpthread|libdl|librt|libgcc_s|libresolv|libanl|libutil|libatomic|ld-linux-[a-z0-9_-]+|libfontconfig|libexpat|libgnutls|libasound|libpulse|libva|libva-drm)\.so(\.[0-9]+)*$'

BAD_DEPS="$(grep -vE "$LINUX_SYS_LIB_RE" <<< "$NEEDED_LIBS" || true)"
if [[ -n "$BAD_DEPS" ]]; then
  echo "ERROR: unexpected dynamic dependencies detected in $OUT_SO:" >&2
  echo "$BAD_DEPS" >&2
  exit 1
fi

echo "==> Checking dynamic relocations via ldd -r (must have zero undefined symbols):"
LDD_OUT="$(ldd -r "$OUT_SO" 2>&1 || true)"
echo "$LDD_OUT"
if grep -q 'undefined symbol:' <<< "$LDD_OUT"; then
  echo "ERROR: undefined symbols detected in $OUT_SO" >&2
  exit 1
fi

echo "==> Verifying exported libmpv symbols:"
EXPORTS="$(nm -D --defined-only "$OUT_SO")"
for sym in mpv_create mpv_initialize mpv_command mpv_render_context_create mpv_render_context_render mpv_render_context_set_update_callback mpv_terminate_destroy; do
  if ! grep -qE "[[:space:]]T[[:space:]]+${sym}(@@.*)?$" <<< "$EXPORTS"; then
    echo "ERROR: missing required exported symbol ${sym}" >&2
    exit 1
  fi
done

echo "==> SHA-256:"
sha256sum "$OUT_SO"
echo "==> Done: $OUT_SO"
