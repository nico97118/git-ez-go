#!/bin/sh
# SPDX-FileCopyrightText: 2026 git-ez contributors
# SPDX-License-Identifier: GPL-3.0-only

# Everything, including the final completeness argument, must arrive before work
# starts. In particular, a truncated curl | sh stream cannot run a partial body.
install_main() {
    set -eu
    umask 077
    LC_ALL=C
    export LC_ALL
    fail() { printf 'git-ez installer: %s\n' "$*" >&2; exit 1; }
    last_argument=
    for last_argument do :; done
    [ "$last_argument" = '__git_ez_installer_complete_v1__' ] || fail 'Incomplete installer; download it again.'
    prefix=
    requested=
    while [ "$#" -gt 1 ]; do
        case $1 in
            --help)
                printf '%s\n' 'Usage: sh install.sh [--version vX.Y.Z[-PRERELEASE]] [--prefix DIRECTORY]' \
                    'Default: latest published stable release, or latest pre-release if no stable exists.' \
                    'Installs git-ez and its manual under $HOME/.local. Requires Git 2.46+.'
                exit 0 ;;
            --version|--prefix)
                [ "$#" -ge 3 ] && [ -n "$2" ] || fail "Missing value for $1. See --help."
                case $1 in --version) requested=$2 ;; --prefix) prefix=$2 ;; esac
                shift 2 ;;
            *) fail "Unknown argument: $1. See --help." ;;
        esac
    done
    if [ -z "$prefix" ]; then
        [ -n "${HOME:-}" ] || fail 'HOME is required for the default prefix; use --prefix.'
        prefix=$HOME/.local
    fi
    valid_tag() { printf '%s\n' "$1" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; }
    if [ -n "$requested" ]; then valid_tag "$requested" || fail 'Version must be an exact release tag such as v0.1.0-alpha.1.'; fi
    for utility in git curl awk grep tar mktemp chmod mv cp mkdir uname; do
        command -v "$utility" >/dev/null 2>&1 || fail "Required utility not found: $utility. Use manual installation."
    done
    git_version=$(git --version) || fail 'Cannot execute Git.'
    printf '%s\n' "$git_version" | awk '
        $1=="git" && $2=="version" {split($3,v,"."); if(v[1]+0>2 || (v[1]+0==2 && v[2]+0>=46)) ok=1}
        END {exit !ok}' || fail 'Git 2.46 or newer is required. Install or upgrade Git yourself, then retry.'
    case $(uname -s) in Linux) system=linux ;; FreeBSD) system=freebsd ;; Darwin) system=darwin ;; *) fail 'Unsupported operating system; supported: Linux, FreeBSD, macOS.' ;; esac
    case $(uname -m) in x86_64|amd64) architecture=amd64 ;; aarch64|arm64) architecture=arm64 ;; *) fail 'Unsupported architecture; supported: amd64 and arm64.' ;; esac
    if command -v sha256sum >/dev/null 2>&1; then
        checksum() { sha256sum "$1" | awk '{print $1}'; }
    elif command -v shasum >/dev/null 2>&1; then
        checksum() { shasum -a 256 "$1" | awk '{print $1}'; }
    elif command -v sha256 >/dev/null 2>&1; then
        checksum() { sha256 -q "$1"; }
    else fail 'A SHA-256 utility is required: sha256sum, shasum or sha256.'; fi
    workspace=$(mktemp -d "${TMPDIR:-/tmp}/git-ez-install.XXXXXXXX") || fail 'Cannot create private temporary directory.'
    staged_binary= staged_manual=
    cleanup() {
        [ -z "$staged_binary" ] || rm -f "$staged_binary"
        [ -z "$staged_manual" ] || rm -f "$staged_manual"
        rm -rf "$workspace"
    }
    trap cleanup EXIT
    trap 'exit 1' HUP INT TERM
    api=https://api.github.com/repos/nico97118/git-ez-go
    download() {
        curl -fsSL --proto '=https' --connect-timeout 15 --max-time 300 -o "$2" "$1" || fail "Download failed: $1. Try again or install manually from GitHub Releases."
    }
    # A small JSON reader, not shell evaluation or regex extraction of JSON.
    # Only release identity/publication fields and asset names are retained.
    parse_releases() {
        awk -v mode="$1" -v target="${system}_${architecture}" -v countfile="$workspace/count" '
        function invalid() {bad=1; exit 1}
        function space() {while(substr(input,pos,1) ~ /[ \t\r\n]/ && pos<=length(input)) pos++}
        function string( c,e,h,n,j,x,out) {
            if(substr(input,pos++,1)!="\"") invalid()
            out=""
            while(pos<=length(input)) {
                c=substr(input,pos++,1)
                if(c=="\"") return out
                if(c ~ /[[:cntrl:]]/) invalid()
                if(c=="\\") {
                    e=substr(input,pos++,1)
                    if(e=="u") {
                        h=substr(input,pos,4); if(h !~ /^[0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F]$/) invalid()
                        n=0;for(j=1;j<=4;j++){x=index("0123456789abcdef",tolower(substr(h,j,1)))-1;n=n*16+x}
                        c=(n>=32 && n<127)?sprintf("%c",n):"?";pos+=4
                    } else if(e=="\"" || e=="\\" || e=="/") c=e
                    else if(e=="b" || e=="f" || e=="n" || e=="r" || e=="t") c="?"
                    else invalid()
                }
                out=out c
            }
            invalid()
        }
        function retain(path,v,type, a,id) {
            split(path,a,".");id=a[1]+0
            if(path ~ /^[0-9]+\.(tag_name|draft|prerelease|published_at)$/) {
                if(seen[path]++) invalid()
                if(a[2]=="tag_name") {if(type!="s") invalid();tag[id]=v; if(id>=count)count=id+1}
                if(a[2]=="draft" || a[2]=="prerelease") {if(type!="b")invalid();flag[path]=v}
                if(a[2]=="published_at") published[id]=v
            }
            if(path ~ /^[0-9]+\.assets\.[0-9]+\.name$/ && type=="s") assets[id SUBSEP v]++
        }
        function value(path, c,key,i,v,type) {
            space();c=substr(input,pos,1)
            if(c=="{") {
                pos++;space();if(substr(input,pos,1)=="}"){pos++;return}
                while(1) {
                    space();key=string();space();if(substr(input,pos++,1)!=":")invalid()
                    value(path "." key);space();c=substr(input,pos++,1)
                    if(c=="}")return;if(c!=",")invalid()
                }
            }
            if(c=="[") {
                pos++;i=0;space();if(substr(input,pos,1)=="]"){pos++;return}
                while(1) {
                    value(path==""?i:path "." i);i++;space();c=substr(input,pos++,1)
                    if(c=="]")return;if(c!=",")invalid()
                }
            }
            if(c=="\"") {v=string();type="s"}
            else if(substr(input,pos,4)=="true") {v="true";type="b";pos+=4}
            else if(substr(input,pos,5)=="false") {v="false";type="b";pos+=5}
            else if(substr(input,pos,4)=="null") {v="";type="null";pos+=4}
            else {
                if(!match(substr(input,pos),/^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?/))invalid()
                v=substr(input,pos,RLENGTH);pos+=RLENGTH;type="n"
            }
            retain(path,v,type)
        }
        {input=input $0 "\n"}
        END {
            if(bad)exit 1
            pos=1;space();if(substr(input,pos,1)!=(mode=="list"?"[":"{"))invalid()
            value(mode=="list"?"":"0");space();if(pos<=length(input))invalid()
            print count+0 > countfile
            for(i=0;i<count;i++) {
                if(!seen[i ".tag_name"] || !seen[i ".draft"] || !seen[i ".prerelease"])invalid()
                if(tag[i] !~ /^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$/)continue
                if(published[i] !~ /^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9:]+Z$/)continue
                name="git-ez_" tag[i] "_" target ".tar.gz"
                printf "%s\t%s\t%s\t%s\t%d\t%d\t%d\n",tag[i],flag[i ".draft"],flag[i ".prerelease"],published[i],assets[i SUBSEP name],assets[i SUBSEP "SHA256SUMS"],assets[i SUBSEP "git-ez_" tag[i] "_source.tar.gz"]
            }
        }' "$2"
    }
    if [ -n "$requested" ]; then
        download "$api/releases/tags/$requested" "$workspace/release.json"
        parse_releases object "$workspace/release.json" > "$workspace/releases" || fail 'Invalid GitHub release metadata.'
        selected=$(awk -F '\t' -v tag="$requested" '$1==tag && $2=="false" {print}' "$workspace/releases")
    else
        : > "$workspace/releases"
        page=1
        while :; do
            download "$api/releases?per_page=100&page=$page" "$workspace/release.json"
            parse_releases list "$workspace/release.json" > "$workspace/page" || fail 'Invalid GitHub release metadata.'
            cat "$workspace/page" >> "$workspace/releases"
            # Count all records, including drafts and unsuitable version tags.
            if [ "$(cat "$workspace/count")" -lt 100 ]; then break; fi
            page=$((page + 1))
        done
        selected=$(awk -F '\t' '$2=="false" {if($3=="false" && $4>stable_date){stable=$0;stable_date=$4} if($3=="true" && $4>pre_date){pre=$0;pre_date=$4}} END{if(stable!="")print stable;else print pre}' "$workspace/releases")
    fi
    [ -n "$selected" ] || fail 'No suitable published release found. Check https://github.com/nico97118/git-ez-go/releases or select --version.'
    [ "$(printf '%s\n' "$selected" | wc -l | tr -d ' ')" = 1 ] || fail 'Ambiguous release metadata.'
    tag=$(printf '%s\n' "$selected" | cut -f1)
    valid_tag "$tag" || fail 'Invalid release tag.'
    if [ -z "$requested" ] && [ "$(printf '%s\n' "$selected" | cut -f3)" = true ]; then
        printf 'Warning: no stable release exists; installing pre-release %s.\n' "$tag" >&2
    fi
    [ "$(printf '%s\n' "$selected" | cut -f5)" = 1 ] && [ "$(printf '%s\n' "$selected" | cut -f6)" = 1 ] && [ "$(printf '%s\n' "$selected" | cut -f7)" = 1 ] || fail 'Required release assets are missing or ambiguous.'
    name=git-ez_${tag}_${system}_${architecture}
    asset=$name.tar.gz
    release=https://github.com/nico97118/git-ez-go/releases/download/$tag
    download "$release/$asset" "$workspace/archive.tar.gz"
    download "$release/SHA256SUMS" "$workspace/SHA256SUMS"
    expected=$(awk -v name="$asset" '$2==name || $2=="*" name {n++;digest=$1} END{if(n!=1 || length(digest)!=64 || digest ~ /[^0-9a-fA-F]/)exit 1;print tolower(digest)}' "$workspace/SHA256SUMS") || fail 'Missing, invalid or ambiguous archive checksum entry.'
    actual=$(checksum "$workspace/archive.tar.gz") || fail 'Cannot compute archive checksum.'
    [ "$actual" = "$expected" ] || fail 'Archive checksum mismatch; existing installation preserved.'
    tar -tzf "$workspace/archive.tar.gz" > "$workspace/names" || fail 'Invalid release archive.'
    tar -tvzf "$workspace/archive.tar.gz" > "$workspace/types" || fail 'Invalid release archive.'
    awk 'substr($0,1,1)!="-" {exit 1}' "$workspace/types" || fail 'Archive must contain only regular files, not links or special files.'
    awk -v root="$name/" '
        index($0,root)!=1 || $0 ~ /[^A-Za-z0-9_.+\/-]/ {exit 1}
        {if(seen[$0]++)exit 1; n=split(substr($0,length(root)+1),parts,"/");for(i=1;i<=n;i++)if(parts[i]=="" || parts[i]=="." || parts[i]=="..")exit 1}
        END{if(NR==0)exit 1}' "$workspace/names" || fail 'Unsafe or duplicate paths in release archive.'
    for required in git-ez docs/man/git-ez.1 LICENSE NOTICE SOURCE.txt BUILD.json SBOM.cdx.json DEPENDENCIES.json docs/installation.md; do
        grep -Fx "$name/$required" "$workspace/names" >/dev/null || fail "Required installation file missing: $required."
    done
    mkdir "$workspace/unpacked"
    tar -xzf "$workspace/archive.tar.gz" -C "$workspace/unpacked" || fail 'Archive extraction failed; existing installation preserved.'
    prepared=$workspace/unpacked/$name
    [ -f "$prepared/git-ez" ] && [ -x "$prepared/git-ez" ] && [ -s "$prepared/docs/man/git-ez.1" ] || fail 'Executable or manual is missing or invalid.'
    [ "$("$prepared/git-ez" --version)" = "git-ez $tag" ] || fail 'Downloaded executable version does not match the selected release or cannot run on this host.'
    case $prefix in /*) ;; *) prefix=$(pwd)/$prefix ;; esac
    destination=$prefix/bin/git-ez
    if [ -e "$destination" ] || [ -L "$destination" ]; then
        [ -f "$destination" ] && [ -x "$destination" ] && [ ! -L "$destination" ] || fail "Refusing to replace non-regular executable: $destination. Inspect it and install manually."
        existing=$("$destination" --version 2>/dev/null) || fail 'Existing executable cannot identify itself; inspect it and install manually.'
        case $existing in 'git-ez '*) ;; *) fail 'Refusing to overwrite an unrelated executable; inspect it and install manually.' ;; esac
    fi
    [ ! -d "$prefix/share/man/man1/git-ez.1" ] && [ ! -L "$prefix/share/man/man1/git-ez.1" ] || fail 'Manual destination must not be a directory or symlink; inspect it and install manually.'
    (umask 022; mkdir -p "$prefix/bin" "$prefix/share/man/man1") || fail 'Cannot create installation directories. Choose a writable --prefix; sudo is never automatic.'
    staged_binary=$(mktemp "$prefix/bin/.git-ez.XXXXXXXX") || fail 'Cannot prepare executable replacement.'
    staged_manual=$(mktemp "$prefix/share/man/man1/.git-ez.XXXXXXXX") || fail 'Cannot prepare manual replacement.'
    cp "$prepared/git-ez" "$staged_binary" && chmod 755 "$staged_binary" || fail 'Cannot prepare executable replacement.'
    cp "$prepared/docs/man/git-ez.1" "$staged_manual" && chmod 644 "$staged_manual" || fail 'Cannot prepare manual replacement.'
    [ "$("$staged_binary" --version)" = "git-ez $tag" ] || fail 'Prepared executable cannot run.'
    # Each rename stays on its destination filesystem. Publish the binary last.
    mv -f "$staged_manual" "$prefix/share/man/man1/git-ez.1" || fail 'Cannot install manual; existing executable preserved.'
    staged_manual=
    mv -f "$staged_binary" "$destination" || fail 'Cannot replace executable.'
    staged_binary=
    [ "$("$destination" --version)" = "git-ez $tag" ] || fail 'Installed executable verification failed.'
    printf 'Installed git-ez %s: %s\nManual: %s/share/man/man1/git-ez.1\n' "$tag" "$destination" "$prefix"
    case :${PATH:-}: in *:"$prefix/bin":*) ;; *) printf 'Add %s/bin to PATH, then run: git ez --version\n' "$prefix" ;; esac
}
install_main "$@" '__git_ez_installer_complete_v1__'
