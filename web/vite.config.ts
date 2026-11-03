import { defineConfig, type Plugin } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { join, relative, sep } from 'node:path';
import { createHash } from 'node:crypto';

// Writes sw.js with the exact list of built files to precache, so the app
// shell loads with no connection. The list is read from disk after the build,
// not from the in-memory bundle: Vite drops some chunks (CSS-only stubs) late,
// and one missing file fails the whole service-worker install.
function serviceWorker(): Plugin {
  let outDir = '';
  return {
    name: 'domestique-sw',
    apply: 'build',
    configResolved(c) {
      outDir = c.build.outDir.startsWith('/') ? c.build.outDir : join(c.root, c.build.outDir);
    },
    writeBundle() {
      const files: string[] = [];
      const walk = (dir: string) => {
        for (const f of readdirSync(dir)) {
          const p = join(dir, f);
          if (statSync(p).isDirectory()) walk(p);
          else files.push(relative(outDir, p).split(sep).join('/'));
        }
      };
      walk(outDir);
      // woff2 only (every target supports it); no maps; not the worker itself.
      const keep = files.filter((f) => !/\.(map|woff)$/.test(f) && f !== 'sw.js' && f !== 'index.html' && f !== '.gitkeep');
      const precache = ['/', ...keep.sort().map((f) => '/' + f)];
      const version = createHash('sha256').update(precache.join('\n')).digest('hex').slice(0, 12);
      const src = readFileSync(new URL('./src/sw.js', import.meta.url), 'utf8')
        .replace('__PRECACHE__', JSON.stringify(precache))
        .replace('__VERSION__', JSON.stringify(version));
      writeFileSync(join(outDir, 'sw.js'), src);
    },
  };
}

export default defineConfig({
  plugins: [svelte(), serviceWorker()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    target: 'safari16',
    assetsInlineLimit: 0,
  },
  server: {
    port: 5173,
    proxy: { '/v1': 'http://127.0.0.1:8099' },
  },
});
