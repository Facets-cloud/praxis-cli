# Web component design and runtime

Use React + Ant Design with the **Facets theme**, not stock AntD with one accent
color or a new hand-coded palette. Preserve the template's React/AntD patch and
bundler configuration until a tested dependency change is requested.

## Theme and shadow root

The base theme is vendored in [theme assets](../assets/web-component-template/src/theme/resolveFacetsTheme.js).
`/public/v1/themeFile` provides a tenant override, not the whole effective theme.
The resolver combines base + tenant (or default fallback), restores component
radii, strips light colors for dark mode and layers dark overrides/algorithm.
Empty/error theme responses fall back to Facets defaults, not bare AntD. These
assets can drift with the product: compare against current source for theme updates.

Read resolved colors, font, radii and spacing through `theme.useToken()`. Use
semantic colors for status, not unrelated chart series. Derive a distinct,
light/dark contrast-tested categorical palette from the active brand color;
`colorLink` may equal `colorPrimary`, making two series indistinguishable.

Mount inside an open shadow root. `StyleProvider container={shadowRoot}` keeps
CSS-in-JS there; `getPopupContainer` must point inside that root too. Position
the popup host relative to the component so dropdowns don't drift on long pages.
Query component elements via the shadow root, not host-page DOM selectors.
Creating DOM nodes via `document.createElement` is fine; traversing/mutating the
host's DOM is the coupling to avoid.

The template observes scope and theme-mode attributes. Explicit theme mode wins;
fallback infers from inherited **text color**, not transparent background-color.
Test mount, attribute change, disconnect/reconnect and repeated bundle loading.
Preserved template code is not proof every lifecycle path has been tested.

## Panel structure and visual meaning

```text
lib/domain   -> pure raw-data interpretation, scoring, absent markers (unit tests)
hooks/data   -> bounded transport, cancellation, required/optional read outcomes
widgets/UI  -> five states and rendered interpretation
```

Use AntD Card/Table/Statistic/Empty/Skeleton/Badge/Alert/Space/Row/Col as appropriate.
Use lucide icons and one chart library where needed, not a second design system.
Keep resource-relative severity and one useful interpretation line near the data.
Missing optional data remains identified as missing; catching it as `[]` without
an absence marker can turn failures into healthy emptiness.

| State | Meaning / display |
|---|---|
| loading | required request still in flight; panel skeleton |
| error | failed request, actionable error without secret leakage |
| no-config | backend/context not configured; name what is missing |
| empty | valid measured response with nothing in selected scope/window |
| data | actual data, with missing fields explicitly marked |

Zero is a legitimate value; decide its health meaning per metric. Zero 5xx can
be good, while an absent error series is unverified. Do not let default status
or an empty array substitute for an unavailable response. Add fixture tests before
implementing interpretation logic; keep timestamps/units and query windows visible.

## Build traps

The platform loads a classic script: build one self-contained **IIFE**, no output
imports/exports/top-level await or split chunks. Source imports are fine. Retain
`inlineDynamicImports`, `cssCodeSplit:false` and the production NODE_ENV define;
Vite library mode otherwise leaves React's process reference unresolved.

Don't switch to UMD: a host page with Monaco's AMD loader can capture it as an
anonymous module, so the custom element is never registered. Register at module
scope and guard duplicate definitions. Confirm the file/tag names line up with
the Raptor registration contract. Build size, syntax and mount behavior each need
their own check. Serve local fixtures over HTTP, not `file://`; test authenticated
data inside the platform separately. Cache/asset URL behavior belongs to Raptor's
delivery reference; don't add speculative query-string cache busters.
