import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import './i18n'
import App from './App.tsx'
import { EventsOn } from '../wailsjs/runtime/runtime'
import { setupBridgeDiagnostics } from './lib/bridgeDiagnostics'

void setupBridgeDiagnostics(EventsOn).catch(() => { /* Diagnostics never block startup. */ })

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
