#!/usr/bin/env bash
# Builds a self-contained, strictly LGPL-2.1-or-later mpv-2.dll for Windows
# (amd64 / x86_64 under MSYS2 UCRT64, or arm64 / aarch64 under MSYS2 CLANGARM64)
# with zero non-system DLL dependencies.
#
# Usage:
#   # From MSYS2 UCRT64 shell (amd64):
#   ./scripts/build-libmpv-windows.sh amd64 dist/mpv-2-windows-amd64.dll
#   # From MSYS2 CLANGARM64 shell (arm64):
#   ./scripts/build-libmpv-windows.sh arm64 dist/mpv-2-windows-arm64.dll
#
# License guarantee:
#   - FFmpeg is configured WITHOUT --enable-gpl, --enable-version3, or --enable-nonfree.
#   - mpv is configured with -Dgpl=false.
#   - Only LGPL-2.1+, MIT, BSD, ISC, FTL, or Zlib static dependencies are linked.
#   - Final verification asserts objdump -p only imports Windows system DLLs.
set -euo pipefail

ARCH="${1:-amd64}"
OUT_DLL_ARG="${2:-dist/mpv-2.dll}"

# Pinned upstream release versions (strictly LGPL-2.1+ / permissive).
ZLIB_VER="1.3.1"
LIBICONV_VER="1.18"
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

pick_tool() {
  for t in "$@"; do
    if command -v "$t" >/dev/null 2>&1; then
      echo "$t"
      return 0
    fi
  done
  echo "$1"
}

AUTOTOOLS_HOST_ARGS=()
case "$ARCH" in
  x86_64|amd64)
    ARCH="x86_64"
    FFMPEG_ARCH="x86_64"
    HOST_TRIPLET="x86_64-w64-mingw32"
    PE_FORMAT_RE="pei-x86-64|coff-x86-64"
    CC="${CC:-gcc}"
    CXX="${CXX:-g++}"
    AR="${AR:-ar}"
    RANLIB="${RANLIB:-ranlib}"
    NM="${NM:-nm}"
    RC="${RC:-windres}"
    STRIP="${STRIP:-strip}"
    OBJDUMP="${OBJDUMP:-objdump}"
    ;;
  arm64|aarch64)
    ARCH="aarch64"
    FFMPEG_ARCH="aarch64"
    HOST_TRIPLET="aarch64-w64-mingw32"
    PE_FORMAT_RE="coff-arm64|pei-aarch64"
    AUTOTOOLS_HOST_ARGS=(--build="$HOST_TRIPLET" --host="$HOST_TRIPLET")
    CC="${CC:-clang}"
    CXX="${CXX:-clang++}"
    AR="${AR:-$(pick_tool llvm-ar ar)}"
    RANLIB="${RANLIB:-$(pick_tool llvm-ranlib ranlib)}"
    NM="${NM:-$(pick_tool llvm-nm nm)}"
    RC="${RC:-$(pick_tool llvm-windres windres)}"
    STRIP="${STRIP:-$(pick_tool llvm-strip strip)}"
    OBJDUMP="${OBJDUMP:-$(pick_tool llvm-objdump objdump)}"
    ;;
  *)
    echo "Unsupported Windows architecture: $ARCH (want amd64, x86_64, arm64, or aarch64)" >&2
    exit 1
    ;;
esac

mkdir -p "$(dirname "$OUT_DLL_ARG")"
OUT_DLL_POSIX="$(cd "$(dirname "$OUT_DLL_ARG")" && pwd)/$(basename "$OUT_DLL_ARG")"

WORK_DIR_RAW="${WORK_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/mygo-libmpv-win.XXXXXX")}"
mkdir -p "$WORK_DIR_RAW"
WORK_DIR_POSIX="$(cygpath -u "$WORK_DIR_RAW")"
WORK_DIR_WIN="$(cygpath -m "$WORK_DIR_RAW")"

SRC_DIR="$WORK_DIR_POSIX/src"
BUILD_DIR_POSIX="$WORK_DIR_POSIX/build"
BUILD_DIR_WIN="$WORK_DIR_WIN/build"
PREFIX_POSIX="$WORK_DIR_POSIX/prefix"
PREFIX_WIN="$WORK_DIR_WIN/prefix"
JOBS="$(nproc)"

mkdir -p "$SRC_DIR" "$BUILD_DIR_POSIX" "$PREFIX_POSIX/include" "$PREFIX_POSIX/lib/pkgconfig"

export CC CXX AR RANLIB NM RC
export CFLAGS="-O2 -I$PREFIX_WIN/include"
export CXXFLAGS="$CFLAGS"
export LDFLAGS="-L$PREFIX_WIN/lib"
# Strictly isolate pkg-config from system /ucrt64 or /clangarm64 pkgconfig dirs
# so no MSYS2 dynamic library (.dll.a) dependencies leak into mpv-2.dll. Use a
# POSIX path here because MSYS2 treats PKG_CONFIG_LIBDIR as a colon-delimited
# path list and translates it to Windows paths for native Win32 pkgconf/meson.
export PKG_CONFIG_LIBDIR="$PREFIX_POSIX/lib/pkgconfig"
export PKG_CONFIG_PATH=""

echo "==> Building strictly LGPL 2.1+ self-contained mpv-2.dll ($MPV_VER) for windows/$ARCH"
echo "    Toolchain:        CC=$CC CXX=$CXX AR=$AR STRIP=$STRIP OBJDUMP=$OBJDUMP"
echo "    Work dir (POSIX): $WORK_DIR_POSIX"
echo "    Work dir (Win):   $WORK_DIR_WIN"
echo "    Output:           $OUT_DLL_POSIX"

fetch_tar() {
  local url="$1"
  local dest="$2"
  if [[ ! -d "$dest" ]]; then
    mkdir -p "$dest"
    local archive="$WORK_DIR_POSIX/$(basename "$url")"
    echo "==> Downloading $url"
    curl -fL --retry 3 --retry-delay 2 -o "$archive" "$url"
    tar -xf "$archive" --strip-components=1 -C "$dest"
    rm -f "$archive"
  fi
}

# Normalize any POSIX MSYS2 prefixes in autotools-generated .pc files into
# forward-slash Windows paths so native Win32 pkgconf/meson/gcc resolve them.
normalize_pc_files() {
  for pc in "$PREFIX_POSIX"/lib/pkgconfig/*.pc; do
    [[ -f "$pc" ]] || continue
    sed -i "s|${PREFIX_POSIX}|${PREFIX_WIN}|g" "$pc"
  done
}

# 1. zlib (Zlib) — static libz.a
fetch_tar "https://github.com/madler/zlib/releases/download/v${ZLIB_VER}/zlib-${ZLIB_VER}.tar.gz" "$SRC_DIR/zlib"
(
  cd "$SRC_DIR/zlib"
  make -f win32/Makefile.gcc CC="$CC" AR="$AR" RC="$RC" -j"$JOBS" libz.a
  cp zlib.h zconf.h "$PREFIX_POSIX/include/"
  cp libz.a "$PREFIX_POSIX/lib/"
  cat > "$PREFIX_POSIX/lib/pkgconfig/zlib.pc" <<EOF
prefix=${PREFIX_WIN}
exec_prefix=\${prefix}
libdir=\${exec_prefix}/lib
includedir=\${prefix}/include

Name: zlib
Description: zlib compression library
Version: ${ZLIB_VER}
Libs: -L\${libdir} -lz
Cflags: -I\${includedir}
EOF
)
pkg-config --cflags --libs zlib

# 2. libiconv (LGPL-2.1+) — static libiconv.a without libintl/NLS
fetch_tar "https://ftp.gnu.org/pub/gnu/libiconv/libiconv-${LIBICONV_VER}.tar.gz" "$SRC_DIR/libiconv"
(
  cd "$SRC_DIR/libiconv"
  ./configure "${AUTOTOOLS_HOST_ARGS[@]}" --prefix="$PREFIX_POSIX" --enable-static --disable-shared --disable-nls --disable-rpath
  make -j"$JOBS" install
)

# 3. FriBidi (LGPL-2.1+)
fetch_tar "https://github.com/fribidi/fribidi/releases/download/v${FRIBIDI_VER}/fribidi-${FRIBIDI_VER}.tar.xz" "$SRC_DIR/fribidi"
meson setup "$BUILD_DIR_WIN/fribidi" "$(cygpath -m "$SRC_DIR/fribidi")" \
  --prefix="$PREFIX_WIN" --libdir=lib --buildtype=release --default-library=static \
  -Ddocs=false -Dtests=false -Dbin=false
ninja -C "$BUILD_DIR_WIN/fribidi" -j"$JOBS" install

# 4. FreeType (FTL)
fetch_tar "https://download.savannah.gnu.org/releases/freetype/freetype-${FREETYPE_VER}.tar.xz" "$SRC_DIR/freetype"
meson setup "$BUILD_DIR_WIN/freetype" "$(cygpath -m "$SRC_DIR/freetype")" \
  --prefix="$PREFIX_WIN" --libdir=lib --buildtype=release --default-library=static \
  -Dharfbuzz=disabled -Dpng=disabled -Dbrotli=disabled -Dbzip2=disabled -Dzlib=external -Dtests=disabled
ninja -C "$BUILD_DIR_WIN/freetype" -j"$JOBS" install

# 5. HarfBuzz (MIT) — with Windows DirectWrite + GDI support
fetch_tar "https://github.com/harfbuzz/harfbuzz/releases/download/${HARFBUZZ_VER}/harfbuzz-${HARFBUZZ_VER}.tar.xz" "$SRC_DIR/harfbuzz"
meson setup "$BUILD_DIR_WIN/harfbuzz" "$(cygpath -m "$SRC_DIR/harfbuzz")" \
  --prefix="$PREFIX_WIN" --libdir=lib --buildtype=release --default-library=static \
  -Dfreetype=enabled -Dgdi=enabled -Ddirectwrite=enabled \
  -Dglib=disabled -Dgobject=disabled -Dcairo=disabled -Dicu=disabled \
  -Dgraphite=disabled -Dgraphite2=disabled -Dtests=disabled -Ddocs=disabled -Dutilities=disabled
ninja -C "$BUILD_DIR_WIN/harfbuzz" -j"$JOBS" install

# 6. libunibreak (Zlib)
fetch_tar "https://github.com/adah1972/libunibreak/releases/download/libunibreak_${LIBUNIBREAK_VER//./_}/libunibreak-${LIBUNIBREAK_VER}.tar.gz" "$SRC_DIR/libunibreak"
(
  cd "$SRC_DIR/libunibreak"
  ./configure "${AUTOTOOLS_HOST_ARGS[@]}" --prefix="$PREFIX_POSIX" --enable-static --disable-shared --disable-dependency-tracking
  make -j"$JOBS" install
)
normalize_pc_files

# 7. libass (ISC) — uses Windows DirectWrite natively for system font lookup
fetch_tar "https://github.com/libass/libass/releases/download/${LIBASS_VER}/libass-${LIBASS_VER}.tar.xz" "$SRC_DIR/libass"
(
  cd "$SRC_DIR/libass"
  ./configure "${AUTOTOOLS_HOST_ARGS[@]}" --prefix="$PREFIX_POSIX" --enable-static --disable-shared \
    --enable-directwrite --disable-fontconfig --disable-libpng --disable-asm
  make -j"$JOBS" install
)
normalize_pc_files

# 8. dav1d (BSD-2-Clause) — fast AV1 decoding
fetch_tar "https://code.videolan.org/videolan/dav1d/-/archive/${DAV1D_VER}/dav1d-${DAV1D_VER}.tar.bz2" "$SRC_DIR/dav1d"
meson setup "$BUILD_DIR_WIN/dav1d" "$(cygpath -m "$SRC_DIR/dav1d")" \
  --prefix="$PREFIX_WIN" --libdir=lib --buildtype=release --default-library=static \
  -Denable_tools=false -Denable_tests=false -Denable_examples=false
ninja -C "$BUILD_DIR_WIN/dav1d" -j"$JOBS" install

# 9. Little CMS 2 (MIT)
fetch_tar "https://github.com/mm2/Little-CMS/releases/download/lcms${LCMS2_VER}/lcms2-${LCMS2_VER}.tar.gz" "$SRC_DIR/lcms2"
meson setup "$BUILD_DIR_WIN/lcms2" "$(cygpath -m "$SRC_DIR/lcms2")" \
  --prefix="$PREFIX_WIN" --libdir=lib --buildtype=release --default-library=static \
  -Djpeg=disabled -Dtiff=disabled -Dutils=false -Dtests=disabled
ninja -C "$BUILD_DIR_WIN/lcms2" -j"$JOBS" install

# 10. libplacebo (LGPL-2.1+) — clone with submodules (fast_float, glad, jinja, markupsafe)
if [[ ! -d "$SRC_DIR/libplacebo" ]]; then
  git clone --depth 1 --branch "$LIBPLACEBO_VER" --recurse-submodules --shallow-submodules \
    https://code.videolan.org/videolan/libplacebo.git "$SRC_DIR/libplacebo"
fi
meson setup "$BUILD_DIR_WIN/libplacebo" "$(cygpath -m "$SRC_DIR/libplacebo")" \
  --prefix="$PREFIX_WIN" --libdir=lib --buildtype=release --default-library=static \
  -Dopengl=enabled -Dvulkan=disabled -Dd3d11=disabled \
  -Dshaderc=disabled -Dglslang=disabled -Dlcms=enabled \
  -Ddemos=false -Dtests=false -Dbench=false -Dfuzz=false \
  -Dunwind=disabled -Dxxhash=disabled -Dlibdovi=disabled
ninja -C "$BUILD_DIR_WIN/libplacebo" -j"$JOBS" install

# 11. FFmpeg (strictly LGPL-2.1+: NO --enable-gpl, NO --enable-version3, NO --enable-nonfree)
# Uses Win32 threads (no libwinpthread), Schannel native TLS, D3D11VA + DXVA2 hwdec, and dav1d.
fetch_tar "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VER}.tar.xz" "$SRC_DIR/ffmpeg"
(
  mkdir -p "$BUILD_DIR_POSIX/ffmpeg"
  cd "$BUILD_DIR_POSIX/ffmpeg"
  "$SRC_DIR/ffmpeg/configure" \
    --prefix="$PREFIX_WIN" \
    --arch="$FFMPEG_ARCH" \
    --target-os=mingw64 \
    --cc="$CC" \
    --cxx="$CXX" \
    --as="$CC" \
    --ar="$AR" \
    --ranlib="$RANLIB" \
    --nm="$NM" \
    --strip="$STRIP" \
    --extra-cflags="$CFLAGS" \
    --extra-ldflags="$LDFLAGS" \
    --pkg-config=pkg-config \
    --pkg-config-flags="--static" \
    --enable-static \
    --disable-shared \
    --enable-w32threads \
    --disable-pthreads \
    --disable-programs \
    --disable-doc \
    --disable-debug \
    --disable-autodetect \
    --enable-zlib \
    --enable-iconv \
    --enable-d3d11va \
    --enable-dxva2 \
    --enable-schannel \
    --enable-libdav1d \
    --enable-network
  make -j"$JOBS" install
)
normalize_pc_files

# 12. mpv / libmpv (strictly LGPL-2.1+: -Dgpl=false)
# Built as a shared DLL (--default-library=shared) while statically linking all
# third-party archives (--prefer-static) and C++/unwind/winpthread runtimes.
if [[ "$ARCH" == "aarch64" ]]; then
  SYS_LIB_DIR="${MSYSTEM_PREFIX:-/clangarm64}/lib"
  STATIC_CPP_TOKENS="-Wl,-Bstatic,-lc++"
  STATIC_RUNTIME_ARGS="'-Wl,-Bstatic','-lc++'"
  if [[ -f "$SYS_LIB_DIR/libc++abi.a" ]]; then
    STATIC_CPP_TOKENS+=",-lc++abi"
    STATIC_RUNTIME_ARGS+=",'-lc++abi'"
  fi
  if [[ -f "$SYS_LIB_DIR/libunwind.a" ]]; then
    STATIC_CPP_TOKENS+=",-lunwind"
    STATIC_RUNTIME_ARGS+=",'-lunwind'"
  fi
  if [[ -f "$SYS_LIB_DIR/libwinpthread.a" ]]; then
    STATIC_CPP_TOKENS+=",-lwinpthread"
    STATIC_RUNTIME_ARGS+=",'-lwinpthread'"
  fi

  # Ensure C++ static archives (libplacebo, harfbuzz) pull static libc++/libunwind
  # even when meson appends Libs.private after our explicit link args. Leaving
  # -Wl,-Bstatic active ensures LLD only links static .a archives (and MinGW's
  # lib*.a system import libs) and never picks up MSYS2 *.dll.a runtime shims.
  if [[ -f "$PREFIX_POSIX/lib/pkgconfig/libplacebo.pc" ]] && ! grep -qE -- '(-lc\+\+|-lstdc\+\+)' "$PREFIX_POSIX/lib/pkgconfig/libplacebo.pc"; then
    sed -i 's|^Libs\.private:|Libs.private: -lc++ |' "$PREFIX_POSIX/lib/pkgconfig/libplacebo.pc"
  fi
  for pc in "$PREFIX_POSIX"/lib/pkgconfig/*.pc; do
    [[ -f "$pc" ]] || continue
    sed -i -E \
      -e "s/(^|[[:space:]])-l(c\+\+|stdc\+\+)([[:space:]]|$)/\\1${STATIC_CPP_TOKENS}\\3/g" \
      -e "s/(^|[[:space:]])-lunwind([[:space:]]|$)/\\1-Wl,-Bstatic,-lunwind\\2/g" \
      -e "s/(^|[[:space:]])-lwinpthread([[:space:]]|$)/\\1-Wl,-Bstatic,-lwinpthread\\2/g" \
      "$pc"
  done

  # Note: LLD for PE/COFF (MinGW) does not support GNU ld's --exclude-libs;
  # mpv already restricts DLL exports via its generated .def file.
  MPV_LINK_ARGS="['-L${PREFIX_WIN}/lib','-static-libgcc',${STATIC_RUNTIME_ARGS}]"
else
  MPV_LINK_ARGS="['-L${PREFIX_WIN}/lib','-static','-static-libgcc','-static-libstdc++','-Wl,-Bstatic','-lstdc++','-lwinpthread','-Wl,-Bdynamic','-Wl,--exclude-libs,ALL']"
fi

fetch_tar "https://github.com/mpv-player/mpv/archive/refs/tags/v${MPV_VER}.tar.gz" "$SRC_DIR/mpv"
meson setup "$BUILD_DIR_WIN/mpv" "$(cygpath -m "$SRC_DIR/mpv")" \
  --prefix="$PREFIX_WIN" --libdir=lib --buildtype=release \
  --default-library=shared \
  --prefer-static \
  -Dgpl=false \
  -Dlibmpv=true \
  -Dcplayer=false \
  -Dbuild-date=false \
  -Dtests=false \
  -Dwin32-threads=enabled \
  -Dwasapi=enabled \
  -Dd3d-hwaccel=enabled \
  -Dd3d9-hwaccel=enabled \
  -Dd3d11=disabled \
  -Ddirect3d=disabled \
  -Dgl=enabled \
  -Dgl-win32=enabled \
  -Dplain-gl=enabled \
  -Dgl-dxinterop=disabled \
  -Degl=disabled \
  -Degl-angle=disabled \
  -Degl-angle-lib=disabled \
  -Degl-angle-win32=disabled \
  -Dvulkan=disabled \
  -Dshaderc=disabled \
  -Dspirv-cross=disabled \
  -Dwin32-smtc=disabled \
  -Diconv=enabled \
  -Dzlib=enabled \
  -Dlcms2=enabled \
  -Dcplugins=disabled \
  -Dlibavdevice=disabled \
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
  -Dc_link_args="$MPV_LINK_ARGS" \
  -Dcpp_link_args="$MPV_LINK_ARGS"
ninja -C "$BUILD_DIR_WIN/mpv" -j"$JOBS" install

BUILT_DLL="$(find "$PREFIX_POSIX/bin" "$PREFIX_POSIX/lib" -maxdepth 1 -name '*mpv*.dll' -type f 2>/dev/null | head -n 1)"
if [[ -z "$BUILT_DLL" ]]; then
  echo "ERROR: libmpv DLL not found in $PREFIX_POSIX/bin or $PREFIX_POSIX/lib" >&2
  exit 1
fi

cp "$BUILT_DLL" "$OUT_DLL_POSIX"
"$STRIP" --strip-unneeded "$OUT_DLL_POSIX"

echo "==> Verifying $OUT_DLL_POSIX"
OBJDUMP_F="$("$OBJDUMP" -f "$OUT_DLL_POSIX" | tr -d '\r')"
echo "$OBJDUMP_F"
if ! grep -qiE "$PE_FORMAT_RE" <<< "$OBJDUMP_F"; then
  echo "ERROR: $OUT_DLL_POSIX PE format does not match $PE_FORMAT_RE" >&2
  exit 1
fi

echo "==> Checking DLL imports via $OBJDUMP -p (must be Windows system DLLs only):"
OBJDUMP_P="$("$OBJDUMP" -p "$OUT_DLL_POSIX" | tr -d '\r')"
IMPORTED_DLLS="$(awk '/DLL Name:/{print $3}' <<< "$OBJDUMP_P" | sort -u)"
echo "$IMPORTED_DLLS"

# Strict allowlist of Windows OS system DLLs (case-insensitive).
WIN_SYS_DLL_RE='^(kernel32|user32|gdi32|advapi32|shell32|ole32|oleaut32|uuid|comdlg32|comctl32|shlwapi|shcore|version|winmm|dwmapi|uxtheme|imm32|ntdll|avrt|powrprof|rpcrt4|cfgmgr32|setupapi|propsys|ws2_32|iphlpapi|secur32|crypt32|bcrypt|ncrypt|dnsapi|mswsock|dwrite|opengl32|d3d11|dxgi|d3d9|dxva2|mfplat|mfuuid|usp10|ucrtbase|msvcrt|api-ms-win-crt-[a-z0-9-]+|api-ms-win-core-[a-z0-9-]+)\.dll$'

BAD_DEPS="$(grep -viE "$WIN_SYS_DLL_RE" <<< "$IMPORTED_DLLS" || true)"
if [[ -n "$BAD_DEPS" ]]; then
  echo "ERROR: non-system DLL dependencies detected in $OUT_DLL_POSIX:" >&2
  echo "$BAD_DEPS" >&2
  exit 1
fi

echo "==> Verifying exported libmpv symbols:"
for sym in mpv_create mpv_initialize mpv_command mpv_render_context_create mpv_render_context_render mpv_render_context_set_update_callback mpv_terminate_destroy; do
  if ! grep -qE "[[:space:]]${sym}([[:space:]]|$)" <<< "$OBJDUMP_P"; then
    echo "ERROR: missing required exported symbol ${sym}" >&2
    exit 1
  fi
done

echo "==> SHA-256:"
sha256sum "$OUT_DLL_POSIX"
echo "==> Done: $OUT_DLL_POSIX"
