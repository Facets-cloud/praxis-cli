# Build a Facets web component

Praxis owns frontend construction here. The **raptor skill's web-components
route** owns registration, UPLOAD/GIT/URL source selection, scope, deployment and
rollback. Read it when the task includes those operations; do not copy an older
curl/registration cookbook into this package.

For implementation read [Design](web-component-design.md) and
[Transport](web-component-transport.md), then adapt
[the template](../assets/web-component-template/package.json). Preserve the
shadow-root, theme resolver and transport patterns; they address platform-specific
failures. Check Node/npm and the template's dependency/tool versions. Build in
the requested repository or an isolated working directory, not in the installed
skill folder. Do not install/upgrade tooling or publish a new repo without scope.

```text
Requirements + data + scope -> template build -> panel fixtures/tests
  -> classic JS bundle -> Raptor registration/delivery -> authenticated UI check
```

Agree what the component should show and who should see it. The Raptor contract
distinguishes environment/project scoped components from NAV_APP. Scoped elements
receive `project-name`/`environment-id`; omitted context is not an empty string.
Do not ask with a picker for context already injected. NAV_APP needs its own
selection UX. `TAB_APP` is deprecated, not a working alternative.

Build the template first, check it mounts in its HTTP-served light/dark fixture,
then add panels with deterministic data fixtures. A local fixture lacks the
control-plane session and cannot verify authenticated data access. A successful
bundle build does not prove actual mounting, viewer permissions or data meaning.

Every panel distinguishes loading, error, no-config, genuine empty and data.
Represent missing metrics as “not collected”, never zero/healthy. Prefer one
required read per panel, with optional data degrading explicitly. Test meaningful
absence/error cases and scope changes, not only a populated screenshot.

The template is a preserved starting point, not a general observability backend
discovery solution. Prometheus/Grafana capability discovery needs current backend
contracts; don't guess endpoints or assume the pod-only observability skill ships
to CLI. Build the supported CP-data portion and explain the gap if that capability
is missing. Alert-rule authoring/application is separate; read-only alert display
does not grant alert mutation authority.

Return source/output paths, element name, scope, endpoints used, tests, missing
capabilities and Raptor delivery evidence separately. When permitted, check the
actual UI through the user's authenticated browser; don't claim visual verification
from compilation alone.
