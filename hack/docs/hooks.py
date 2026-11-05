# Copyright 2026 Oreon IT.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""MkDocs hooks: generate the CRD reference and release notes, and fill in the release version."""

import os
import re
import runpy
import shutil

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))


def release_version():
    """The VERSION from the Makefile, which the release bump keeps current."""
    with open(os.path.join(ROOT, "Makefile")) as f:
        m = re.search(r"^VERSION \?= (\S+)$", f.read(), re.MULTILINE)
    return m.group(1)


def on_pre_build(config, **kwargs):
    runpy.run_path(os.path.join(ROOT, "hack", "docs", "gen-reference.py"), run_name="__main__")
    shutil.copyfile(os.path.join(ROOT, "CHANGELOG.md"), os.path.join(ROOT, "docs", "changelog.md"))


def on_page_markdown(markdown, page, config, files, **kwargs):
    return markdown.replace("{{ version }}", release_version())
