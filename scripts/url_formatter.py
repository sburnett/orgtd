#!/usr/bin/env python3
"""Test formatter for orgtd's --url-formatter: wraps a URL with no title.

Supports both invocation modes orgtd uses:
  - single-URL mode ("<prog> <url>"), for live in-editor formatting
  - batch mode ("<prog>", no url argument): reads URLs one per line from
    stdin and prints the same number of formatted lines to stdout, for
    :format-links
"""

import sys

if len(sys.argv) > 1:
    print(f"[[{sys.argv[1]}]]")
else:
    for line in sys.stdin:
        url = line.rstrip("\r\n")
        print(f"[[{url}]]")
