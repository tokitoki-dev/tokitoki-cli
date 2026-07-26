#!/usr/bin/env python3
"""Regenerate internal/usage/windows_zones.go from the Go toolchain's own data.

Go ships a Windows-zone table (zoneinfo_abbrs_windows.go) generated from the
Unicode CLDR windowsZones.xml, with the IANA name in a trailing comment. Reading
it here keeps the mapping in step with whatever Go version builds the CLI,
rather than vendoring a second copy of CLDR that would drift.

Run after a Go upgrade:  python3 scripts/gen-windows-zones.py
"""
import os
import re
import subprocess

goroot = subprocess.run(["go", "env", "GOROOT"], capture_output=True, text=True).stdout.strip()
source = os.path.join(goroot, "src/time/zoneinfo_abbrs_windows.go")
pairs = re.findall(r'"([^"]+)":\s*\{[^}]*\},\s*//\s*(\S+)', open(source).read())
if not pairs:
    raise SystemExit(f"no zone pairs found in {source}")

lines = [
    "// Code generated from Go's zoneinfo_abbrs_windows.go (itself generated from",
    "// the Unicode CLDR windowsZones.xml). DO NOT EDIT.",
    "// Regenerate with: python3 scripts/gen-windows-zones.py",
    "",
    "package usage",
    "",
    "// windowsToIANA maps a Windows time zone key name, as stored in the registry",
    "// under HKLM\\SYSTEM\\CurrentControlSet\\Control\\TimeZoneInformation, to the",
    "// IANA name the rest of the system speaks.",
    "//",
    "// Windows does not use IANA zones; it keeps its own list (\"Tokyo Standard",
    "// Time\"), and the correspondence is maintained by the Unicode CLDR project.",
    "// This table is the only way to obtain an IANA name on Windows: Go's",
    "// time.Local reports the literal string \"Local\" on every platform, and the",
    "// zone abbreviations it does expose (\"JST\") are ambiguous across regions.",
    "var windowsToIANA = map[string]string{",
]
for windows_name, iana in sorted(pairs):
    lines.append(f'\t"{windows_name}": "{iana}",')
lines.append("}")

target = os.path.join(os.path.dirname(__file__), "..", "internal/usage/windows_zones.go")
with open(target, "w") as handle:
    handle.write("\n".join(lines) + "\n")
subprocess.run(["gofmt", "-w", target], check=True)
print(f"wrote {len(pairs)} mappings to internal/usage/windows_zones.go")
