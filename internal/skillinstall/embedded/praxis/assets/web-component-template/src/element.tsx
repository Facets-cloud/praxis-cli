/**
 * Custom-element shell. Copy this verbatim; the details are load-bearing.
 *
 * Mounts React into an OPEN SHADOW ROOT, styles it with the Facets AntD theme,
 * and reads the page context the control-plane drawer injects. Verified in
 * headless Chromium: antd renders, recharts measures, popups stay contained, and
 * nothing leaks either direction.
 */

import '@ant-design/v5-patch-for-react-19';
import React, { useEffect, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { StyleProvider } from '@ant-design/cssinjs';
import { Alert, Card, Space, Typography } from 'antd';
import { ConfigProvider } from 'antd';
import { fetchTenantTheme, resolveFacetsTheme } from './theme/resolveFacetsTheme.js';

/**
 * The tag this element registers. RENAME IT for your component — the host reads
 * whatever name you pass to `customElements.define`, so nothing else needs editing.
 */
const TAG = 'my-web-component';

interface Ctx {
  projectName?: string;
  environmentId?: string;
  dark: boolean;
}

/**
 * Placeholder. Replace with your own `<App />` — see `design-language.md` for the
 * lib/hooks/widgets split and the five states every panel must handle.
 *
 * It exists so the template builds and renders before you write a single widget.
 * Run the build first and confirm you see this; then you know the toolchain, the
 * shadow root and the theme all work, and any later blank panel is your code.
 */
const Placeholder: React.FC<{ ctx: Ctx }> = ({ ctx }) => (
  <Card size="small" title="Facets web component">
    <Space direction="vertical" size={8} style={{ width: '100%' }}>
      <Typography.Text type="secondary">
        project={ctx.projectName ?? '(none)'} · environment={ctx.environmentId ?? '(none)'}
      </Typography.Text>
      {!ctx.environmentId && (
        // A SCOPED component is told where it is. Do not add project/environment
        // pickers — only a NAV_APP registration needs them, because only it has no
        // injected context.
        <Alert
          type="info"
          showIcon
          message="No environment context"
          description="Register with type ENVIRONMENT and a scopeDetails target, or pass environment-id."
        />
      )}
    </Space>
  </Card>
);

const Root: React.FC<{ ctx: Ctx; popupHost: HTMLElement }> = ({ ctx, popupHost }) => {
  const [tenantTheme, setTenantTheme] = useState<object | null>(null);

  // Tenant override is a PUBLIC endpoint — no session needed. Until it lands we
  // render on the vendored base, which is still the Facets look, never stock antd.
  useEffect(() => {
    let live = true;
    fetchTenantTheme().then((t) => live && setTenantTheme(t));
    return () => {
      live = false;
    };
  }, []);

  return (
    <ConfigProvider
      theme={resolveFacetsTheme({ dark: ctx.dark, tenantTheme })}
      getPopupContainer={() => popupHost}
    >
      <Placeholder ctx={ctx} />
    </ConfigProvider>
  );
};

class ObservabilityDashboard extends HTMLElement {
  static get observedAttributes() {
    return ['project-name', 'environment-id', 'theme-mode'];
  }

  private root?: Root;
  private shadow?: ShadowRoot;
  private wrapper?: HTMLElement;
  private mount?: HTMLElement;
  private popupHost?: HTMLElement;

  connectedCallback() {
    if (!this.shadow) {
      this.shadow = this.attachShadow({ mode: 'open' });

      // A positioned wrapper, with the popup host pinned to its origin at zero
      // height. antd computes popup offsets against the container, so anchoring
      // it to the component's own origin keeps dropdowns from drifting on a tall
      // or scrolled card.
      this.wrapper = document.createElement('div');
      this.wrapper.style.position = 'relative';

      this.mount = document.createElement('div');
      this.popupHost = document.createElement('div');
      Object.assign(this.popupHost.style, {
        position: 'absolute',
        top: '0',
        left: '0',
        width: '100%',
        height: '0',
      });

      this.wrapper.append(this.popupHost, this.mount);
      this.shadow.append(this.wrapper);
      this.root = createRoot(this.mount);
    }
    this.draw();
  }

  attributeChangedCallback() {
    if (this.shadow) this.draw();
  }

  disconnectedCallback() {
    this.root?.unmount();
    this.root = undefined;
    this.shadow = undefined;
  }

  /**
   * Infer dark mode from the INHERITED TEXT COLOUR.
   *
   * `color` is an inherited property so it reaches this element from the host.
   * `background-color` is not — this element's own background always computes to
   * rgba(0,0,0,0), and a luminance test reads transparent as black, rendering
   * every component dark on a light host. That is the "dark component on a light
   * drawer" bug, and it is one line deep.
   */
  private isDark(): boolean {
    const attr = this.getAttribute('theme-mode');
    if (attr) return attr === 'dark';
    const rgb = getComputedStyle(this).color.match(/[\d.]+/g);
    if (!rgb || rgb.length < 3) return false;
    const [r, g, b] = rgb.map(Number);
    return 0.299 * r + 0.587 * g + 0.114 * b > 128; // light text => dark surface
  }

  private draw() {
    // Absent levels are OMITTED by the host, not empty-stringed. Check presence.
    const ctx: Ctx = {
      projectName: this.getAttribute('project-name') ?? undefined,
      environmentId: this.getAttribute('environment-id') ?? undefined,
      dark: this.isDark(),
    };

    this.root?.render(
      <StyleProvider container={this.shadow!} hashPriority="high">
        <Root ctx={ctx} popupHost={this.popupHost!} />
      </StyleProvider>
    );
  }
}

// Idempotent: the host may load the bundle more than once per page.
if (!customElements.get(TAG)) customElements.define(TAG, ObservabilityDashboard);
