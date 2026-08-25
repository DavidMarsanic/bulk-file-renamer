#!/bin/bash
# Securexe stages the built binary in as "bulk-file-renamer-bin" next to
# this script. Bare invocation opens the browser UI directly — no terminal
# usage dump — so this just execs straight through.
cd "$(dirname "$0")"
exec ./bulk-file-renamer-bin
