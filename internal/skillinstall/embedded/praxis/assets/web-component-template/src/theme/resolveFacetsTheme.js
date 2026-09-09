/**
 * Resolve the Facets AntD theme for a web component.
 *
 * A faithful port of `control-plane-ui-react/src/hooks/useThemeLoader.ts` +
 * `src/utils/theme.utils.ts`. Feed the result straight to `<ConfigProvider theme={...}>`.
 *
 * Why this exists: `/public/v1/themeFile` serves only the TENANT OVERRIDE. The
 * Facets base theme lives in TypeScript inside the React app, so a component that
 * fetches the endpoint alone renders stock Ant Design on any tenant without a
 * custom theme — wrong radii, wrong control height, wrong font, wrong surfaces.
 * The vendored JSON beside this file is that base.
 *
 * Keep the step order. Each step fixes a real defect, and step 3 in particular
 * looks redundant until you see Inputs with the wrong corner radius.
 *
 * The React app has a fourth step this port omits on purpose: it also writes the
 * theme's top-level keys out as `--custom-properties` for the deprecated Angular
 * UI. Nothing here consumes `var(--...)` — widgets read tokens through
 * `theme.useToken()` — so those keys are not vendored.
 */

import { theme as antTheme } from 'antd';

import FACETS_BASE from './facets-base.json';
import FACETS_DARK_OVERRIDES from './facets-dark-overrides.json';
import FACETS_DEFAULT_OVERRIDE from './facets-default-override.json';

/** Token shallow-merge + per-component shallow-merge. Override wins. */
function deepMergeTheme(defaultTheme, apiTheme) {
  const result = { ...defaultTheme };

  if (apiTheme.token || defaultTheme.token) {
    result.token = { ...defaultTheme.token, ...apiTheme.token };
  }

  if (apiTheme.components || defaultTheme.components) {
    result.components = { ...defaultTheme.components };
    if (apiTheme.components) {
      for (const key of Object.keys(apiTheme.components)) {
        result.components[key] = {
          ...(defaultTheme.components?.[key] || {}),
          ...(apiTheme.components?.[key] || {}),
        };
      }
    }
  }

  if (apiTheme.algorithm) result.algorithm = apiTheme.algorithm;

  return result;
}

const isHex = (v) => typeof v === 'string' && /^#[0-9a-fA-F]{3,8}$/.test(v);

/**
 * Fetch the tenant override. PUBLIC endpoint — no auth, no CP session needed.
 * Returns null on anything unexpected so the caller falls back to the default
 * override rather than to stock AntD.
 */
export async function fetchTenantTheme(origin = window.location.origin) {
  try {
    const res = await fetch(`${origin}/public/v1/themeFile`);
    if (!res.ok) return null;
    const body = await res.json();
    if (!body?.content) return null;
    const parsed = JSON.parse(body.content);
    return parsed && Object.keys(parsed).length > 0 ? parsed : null;
  } catch {
    return null;
  }
}

/**
 * @param {object}  opts
 * @param {boolean} opts.dark        render the dark variant
 * @param {object=} opts.tenantTheme parsed `/public/v1/themeFile` content, or null
 * @returns {object} an antd ThemeConfig
 */
export function resolveFacetsTheme({ dark = false, tenantTheme = null } = {}) {
  // 1 + 2 — base ⊕ override. An absent tenant theme falls back to the same
  // in-code default the React app uses, NOT to an empty object.
  const override = tenantTheme && Object.keys(tenantTheme).length > 0
    ? tenantTheme
    : FACETS_DEFAULT_OVERRIDE;

  const base = deepMergeTheme(FACETS_BASE, override);

  // 3 — the override sets Input/Select borderRadius: 4, which beats the global
  // token. Put the base radii back at component level so they win.
  const radii = {
    borderRadius: FACETS_BASE.token.borderRadius,
    borderRadiusSM: FACETS_BASE.token.borderRadiusSM,
    borderRadiusLG: FACETS_BASE.token.borderRadiusLG,
  };
  base.components = {
    ...base.components,
    Button: { ...base.components?.Button, ...radii },
    Input: { ...base.components?.Input, ...radii },
    Select: { ...base.components?.Select, ...radii },
  };

  if (!dark) return base;

  // 4 — dark is NOT a second hand-authored palette. Strip every colour from the
  // light theme so antd's darkAlgorithm can generate surfaces, keep the brand
  // accent and all non-colour tokens, then layer the dark overrides on top.
  const isColorTokenKey = (k, v) =>
    k.startsWith('color') || k.startsWith('Color') || /Bg$|Background|Border/.test(k) || isHex(v);

  const cleanToken = {};
  for (const [k, v] of Object.entries(base.token || {})) {
    if (!isColorTokenKey(k, v)) cleanToken[k] = v;
  }
  if (base.token?.colorPrimary) cleanToken.colorPrimary = base.token.colorPrimary;

  const isComponentColorKey = (k, v) => /color|Color|Bg$|Background/.test(k) || isHex(v);

  const cleanComponents = {};
  for (const [comp, overrides] of Object.entries(base.components || {})) {
    const cleaned = {};
    for (const [k, v] of Object.entries(overrides || {})) {
      if (!isComponentColorKey(k, v)) cleaned[k] = v;
    }
    if (Object.keys(cleaned).length > 0) cleanComponents[comp] = cleaned;
  }

  const darkComponents = { ...cleanComponents };
  for (const [comp, overrides] of Object.entries(FACETS_DARK_OVERRIDES.components || {})) {
    darkComponents[comp] = { ...(cleanComponents[comp] || {}), ...overrides };
  }

  return {
    ...base,
    token: { ...cleanToken, ...FACETS_DARK_OVERRIDES.token },
    components: darkComponents,
    algorithm: antTheme.darkAlgorithm,
  };
}
