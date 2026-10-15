#!/bin/sh
# Remove only the installed executable. Configuration and conversations stay.
set -eu
install_dir=${YMZ_INSTALL_DIR:-"${HOME}/.local/bin"}
rm -f "${install_dir}/ymz"
printf 'Removed %s/ymz; configuration and core.db were kept.\n' "$install_dir"
