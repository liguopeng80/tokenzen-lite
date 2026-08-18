import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

function securityHeaders(): Plugin {
  return {
    name: 'security-headers',
    configureServer(server) {
      server.middlewares.use((_req, res, next) => {
        res.setHeader('X-Frame-Options', 'DENY');
        res.setHeader('X-Content-Type-Options', 'nosniff');
        res.setHeader('Referrer-Policy', 'strict-origin-when-cross-origin');
        res.setHeader(
          'Content-Security-Policy',
          "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self'; connect-src 'self' ws: http://localhost:19030;",
        );
        next();
      });
    },
  };
}

export default defineConfig({
  plugins: [react(), securityHeaders()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    host: '0.0.0.0',
    port: 18974,
    proxy: {
      '/api': {
        target: 'http://localhost:19030',
        changeOrigin: true,
      },
      '/v1': {
        target: 'http://localhost:19030',
        changeOrigin: true,
      },
      // 状态页直连厂商 Statuspage 域名（响应带 CORS 头），开发与生产行为一致，
      // 因此不再为其配置开发代理——代理只在开发模式存在，会掩盖生产环境的真实表现。
    },
  },
});
