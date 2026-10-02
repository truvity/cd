#!/usr/bin/env python3
"""Reads a `helm template` render on stdin, writes it back with every
CustomResourceDefinition replaced by one comment line carrying its name and
a sha256 of its text.

Why: the upstream charts render their CRDs inline, and they are ~95% of the
bytes (1.9 MB per Argo CD golden). A golden that large is a golden nobody
reads, and a diff in it is lost. The digest keeps the one property that
matters: a CRD that changes in any way moves the golden, and the reviewer
sees WHICH one. What the CRD says is the upstream's to review, in its own
changelog.
"""
import hashlib
import re
import sys

docs = re.split(r"(?m)^---\n", sys.stdin.read())
out = []
for d in docs:
    if re.search(r"(?m)^kind: CustomResourceDefinition$", d):
        m = re.search(r"(?m)^  name: (\S+)$", d)
        name = m.group(1) if m else "?"
        out.append(f"# CustomResourceDefinition {name} sha256:{hashlib.sha256(d.encode()).hexdigest()}\n")
    else:
        out.append(d)
sys.stdout.write("---\n".join(out))
