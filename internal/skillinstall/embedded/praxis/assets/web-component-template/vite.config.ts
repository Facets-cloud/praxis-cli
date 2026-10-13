import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

/**
 * Single-file bundle. The platform serves one file per component, so the build must
 * inline everything — no code splitting, no separate stylesheet.
 *
 * Measured: ~1.0 MB raw / ~315 KB gzip against an 8 MB server cap. There is room,
 * but not room to be careless — do not add a second chart library.
 */
export default defineConfig({
  plugins: [react()],
  define: {
    // Vite's lib mode does NOT substitute this, and React reads it at module
    // scope. Without it you get `process is not defined` and a blank panel with
    // no console error — one of the four silent-blank traps.
    'process.env.NODE_ENV': JSON.stringify('production'),
  },
  build: {
    lib: {
      // RENAME `name` and `fileName` for your component. `fileName` is the value
      // you give as `entryFile` when you register the component.
      entry: 'src/element.tsx',
      formats: ['iife'],
      name: 'MyWebComponent',
      fileName: () => 'my-web-component.js',
    },
    rollupOptions: {
      // One file, no code-splitting: the served bundle is a single asset.
      output: { inlineDynamicImports: true },
    },
    cssCodeSplit: false,
    minify: 'esbuild',
    target: 'es2020',
    reportCompressedSize: true,
  },
  test: {
    // The pure layer needs no DOM. Keep it that way — it is why these tests can
    // run against real production fixtures in milliseconds.
    environment: 'node',
    include: ['src/**/*.test.{js,jsx,ts,tsx}'],
  },
});
