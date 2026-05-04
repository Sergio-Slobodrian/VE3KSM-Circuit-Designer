import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Circuit Lab ships only plain JavaScript to the browser. We use the React
// plugin solely for JSX transformation; no TypeScript pipeline is wired up,
// and *.ts/*.tsx files are intentionally absent from src/.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/ws':  { target: 'ws://localhost:8080', ws: true },
    },
  },
});
