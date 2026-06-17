#!/usr/bin/env python3
"""
Extract every Yilt source file from yiltspec.html into /home/z/my-project/yiltc/.

The spec uses three different layouts for embedding source files, but pandoc
re-parses .md content as markdown, which splits files across multiple HTML
elements.  The reliable strategy is:

1. Find EVERY file header in the spec, regardless of layout:
   - Layout A/B (HTML): <h2 ...><code>path/to/file.go</code> — N lines</h2>
   - Layout C (Markdown text inside <pre><code>):
        ## `path/to/file.ext` — N lines
        ```lang
   Both yield (path, declared_lines, start_offset, end_of_header_offset).

2. Sort all headers by their offset in the file.  The "content span" for
   each file is from the end of its header to the start of the next file's
   header (or to the next chapter <h1> with NN- prefix, whichever comes
   first).

3. Extract the content from that span by stripping all HTML tags and
   HTML-unesaping.  Then take the first `declared` lines — this is the
   authoritative content size.
"""

import html
import os
import re
import sys
from pathlib import Path

SRC_HTML = "/home/z/my-project/upload/yiltspec.html"
DEST_ROOT = Path("/home/z/my-project/yiltc")

# Layout A/B: HTML <h2> header
HEADER_AB_RE = re.compile(
    r'<h[1-6][^>]*>\s*<code>([^<]+)</code>\s*—\s*(\d+)\s*lines\s*</h[1-6]>',
    re.DOTALL,
)

# Layout C: Markdown-form header inside a <pre><code> block
# Pattern: line starts with `## \`path\` — N lines` and is followed by ```lang
HEADER_C_RE = re.compile(
    r'^##\s+`([^`]+)`\s+[—-]\s+(\d+)\s+lines\s*\n```[a-zA-Z0-9]*\s*\n',
    re.MULTILINE,
)

# Chapter h1 with NN- prefix (real chapter boundary, not pandoc-generated
# from .gitignore content)
CHAPTER_H1_RE = re.compile(r'<h1\b[^>]*>\s*\d{1,2}-', re.DOTALL)


def strip_tags(s: str) -> str:
    return re.sub(r'<[^>]+>', '', s)


def unescape(s: str) -> str:
    return html.unescape(s)


def normalise(text: str) -> str:
    text = text.replace('\r\n', '\n').replace('\r', '\n')
    if not text.endswith('\n'):
        text += '\n'
    return text


def find_all_headers(data: str):
    """Find every file header in the spec (Layout A/B and Layout C).
    Returns a sorted list of dicts:
        {path, declared, header_start, content_start}
    `header_start` is where the header itself begins (used as a boundary
    for the PREVIOUS file).  `content_start` is where the file's content
    begins (used as the start of THIS file's extraction).
    """
    headers = []
    # Layout A/B
    for m in HEADER_AB_RE.finditer(data):
        headers.append({
            'path': m.group(1).strip(),
            'declared': int(m.group(2)),
            'header_start': m.start(),
            'content_start': m.end(),
        })
    # Layout C — we need to scan inside <pre><code> blocks for the markdown
    # header pattern.
    for pm in re.finditer(r'<pre><code>(.*?)</code></pre>', data, re.DOTALL):
        block_start = pm.start(1)  # offset of inner content in `data`
        inner_raw = pm.group(1)
        # Build a stripped-text version with a mapping back to raw offsets.
        stripped = []
        raw_positions = []  # raw_positions[i] = offset in `data` for stripped char i
        i = 0
        while i < len(inner_raw):
            if inner_raw[i] == '<':
                j = inner_raw.find('>', i)
                if j == -1:
                    break
                i = j + 1
            else:
                stripped.append(inner_raw[i])
                raw_positions.append(block_start + i)
                i += 1
        stripped_text = ''.join(stripped)
        for hm in HEADER_C_RE.finditer(stripped_text):
            path = hm.group(1).strip()
            declared = int(hm.group(2))
            # header_start = where "## `path`" begins in the raw data
            header_start_in_stripped = hm.start()
            content_start_in_stripped = hm.end()
            # Clamp to available raw positions
            if header_start_in_stripped < len(raw_positions):
                header_start = raw_positions[header_start_in_stripped]
            else:
                header_start = block_start + len(inner_raw)
            if content_start_in_stripped < len(raw_positions):
                content_start = raw_positions[content_start_in_stripped]
            else:
                content_start = block_start + len(inner_raw)
            headers.append({
                'path': path,
                'declared': declared,
                'header_start': header_start,
                'content_start': content_start,
            })
    # Sort by header_start
    headers.sort(key=lambda h: h['header_start'])
    return headers


def find_next_boundary(data: str, start: int, all_headers: list, idx: int):
    """Find where this file's content ends.  It's the min of:
       - The start of the next file HEADER (the header_start, not
         content_start, so we don't include the next file's header line).
       - The next chapter <h1> with NN- prefix.
       - The end of the data.
    """
    candidates = []
    if idx + 1 < len(all_headers):
        candidates.append(all_headers[idx + 1]['header_start'])
    # Find next chapter h1
    m = CHAPTER_H1_RE.search(data, pos=start)
    if m:
        candidates.append(m.start())
    if not candidates:
        return len(data)
    return min(candidates)


def main():
    print(f"Reading {SRC_HTML} ...", file=sys.stderr)
    with open(SRC_HTML, encoding='utf-8') as f:
        data = f.read()
    print(f"  size: {len(data):,} bytes", file=sys.stderr)

    print("\nFinding all file headers ...", file=sys.stderr)
    headers = find_all_headers(data)
    print(f"  found {len(headers)} headers", file=sys.stderr)

    # Deduplicate headers by path — keep the earliest one (smallest offset).
    seen = {}
    for h in headers:
        if h['path'] not in seen:
            seen[h['path']] = h
        else:
            if h['content_start'] < seen[h['path']]['content_start']:
                seen[h['path']] = h
    headers = sorted(seen.values(), key=lambda h: h['content_start'])
    print(f"  unique paths: {len(headers)}", file=sys.stderr)

    print("\nExtracting content ...", file=sys.stderr)
    files = []  # (path, declared, content)
    for i, h in enumerate(headers):
        path = h['path']
        declared = h['declared']
        start = h['content_start']
        end = find_next_boundary(data, start, headers, i)
        chunk = data[start:end]
        # Strip HTML tags and unescape
        text = unescape(strip_tags(chunk))
        text = text.lstrip('\n')
        # Take first `declared` lines (the authoritative content size).
        lines = text.split('\n')
        content = '\n'.join(lines[:declared])
        content = normalise(content)
        files.append((path, declared, content))

    # Write to disk
    print(f"\nWriting to {DEST_ROOT} ...", file=sys.stderr)
    written = 0
    skipped_binary = 0
    for path, declared, content in files:
        if path.startswith('/'):
            print(f"  SKIP absolute path: {path}", file=sys.stderr)
            continue
        # testsuite/basic/print is the compiled ELF binary output, not source
        if path == 'testsuite/basic/print':
            skipped_binary += 1
            continue
        dest = DEST_ROOT / path
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text(content, encoding='utf-8')
        written += 1

    print(f"\nDone. {written}/{len(files)} files written. ({skipped_binary} binary skipped)", file=sys.stderr)

    # Report mismatches
    mismatches = []
    for path, declared, content in files:
        if path == 'testsuite/basic/print':
            continue
        actual = content.count('\n')
        if abs(actual - declared) > 3:
            mismatches.append((path, declared, actual, actual - declared))
    if mismatches:
        mismatches.sort(key=lambda x: -abs(x[3]))
        print(f"\nLine count mismatches ({len(mismatches)} files):", file=sys.stderr)
        for path, declared, actual, delta in mismatches[:30]:
            print(f"  {path}: declared={declared} actual={actual} (delta={delta:+d})", file=sys.stderr)


if __name__ == '__main__':
    main()
