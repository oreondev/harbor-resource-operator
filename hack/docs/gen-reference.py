#!/usr/bin/env python3
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

"""Write one Markdown reference page per CRD into docs/reference/.

The pages are built from config/crd/bases (fields, types, defaults,
descriptions) and config/samples (the example), so they always match the
release. The output is generated at docs build time and not committed.
"""

import glob
import os
import sys

import yaml

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
OUT = os.path.join(ROOT, "docs", "reference")


def cell(text):
    """Make text safe for a Markdown table cell."""
    return " ".join(str(text).split()).replace("|", "\\|")


def type_of(prop):
    if "enum" in prop:
        return " \\| ".join(f"`{v}`" for v in prop["enum"])
    t = prop.get("type", "object")
    if t == "array":
        return f"[]{type_of(prop.get('items', {}))}"
    if prop.get("x-kubernetes-int-or-string"):
        return "int or string"
    return prop.get("format", t)


def rows(props, required, prefix=""):
    """Flatten an OpenAPI object into (path, type, required, default, description) rows."""
    for name in sorted(props):
        prop = props[name]
        path = f"{prefix}{name}"
        default = prop.get("default")
        yield (path, type_of(prop), name in required, "" if default is None else yaml.safe_dump(default, default_flow_style=True).strip().removesuffix("..."), prop.get("description", ""))
        child = prop.get("items", {}) if prop.get("type") == "array" else prop
        if child.get("properties") and path.split(".")[-1] != "conditions":
            suffix = "[]." if prop.get("type") == "array" else "."
            yield from rows(child["properties"], set(child.get("required", [])), path + suffix)


def table(props, required):
    out = ["| Field | Type | Required | Default | Description |", "|---|---|---|---|---|"]
    for path, typ, req, default, desc in rows(props, required):
        out.append(f"| `{path}` | {cell(typ)} | {'yes' if req else ''} | {f'`{cell(default)}`' if default else ''} | {cell(desc)} |")
    return "\n".join(out)


def page(crd, samples):
    names = crd["spec"]["names"]
    version = crd["spec"]["versions"][0]
    schema = version["schema"]["openAPIV3Schema"]
    spec = schema["properties"].get("spec", {})
    status = schema["properties"].get("status", {})
    kind = names["kind"]
    lines = [
        "---",
        f"title: {kind}",
        f"description: {cell(schema.get('description', kind))} Field reference and example for the Harbor Resource Operator.",
        "---",
        "",
        f"# {kind}",
        "",
        schema.get("description", ""),
        "",
        f"API version `{crd['spec']['group']}/{version['name']}`, {crd['spec']['scope'].lower()}, "
        f"short name{'s' if len(names.get('shortNames', [])) > 1 else ''} "
        + ", ".join(f"`{s}`" for s in names.get("shortNames", [])) + ".",
        "",
    ]
    sample = samples.get(names["singular"])
    if sample:
        lines += ["## Example", "", "```yaml", sample.strip(), "```", ""]
    lines += ["## Spec", "", table(spec.get("properties", {}), set(spec.get("required", []))), ""]
    if status.get("properties"):
        lines += ["## Status", "", table(status["properties"], set()), ""]
    cols = version.get("additionalPrinterColumns", [])
    if cols:
        lines += [f"## `kubectl get {names['plural']}` columns", "", "| Column | Source |", "|---|---|"]
        lines += [f"| {c['name']} | `{cell(c['jsonPath'])}` |" for c in cols]
        lines.append("")
    return kind, "\n".join(lines)


def main():
    samples = {}
    for path in glob.glob(os.path.join(ROOT, "config", "samples", "harbor_v1alpha1_*.yaml")):
        singular = os.path.basename(path)[len("harbor_v1alpha1_"):-len(".yaml")]
        samples[singular] = open(path).read()
    os.makedirs(OUT, exist_ok=True)
    kinds = []
    for path in sorted(glob.glob(os.path.join(ROOT, "config", "crd", "bases", "*.yaml"))):
        crd = yaml.safe_load(open(path))
        kind, text = page(crd, samples)
        with open(os.path.join(OUT, kind.lower() + ".md"), "w") as f:
            f.write(text)
        kinds.append(kind)
    print(f"wrote {len(kinds)} reference pages to {os.path.relpath(OUT, ROOT)}", file=sys.stderr)


if __name__ == "__main__":
    main()
