import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { installBridgeFallback } from './core/bridge-fallback'
import './styles/global.css'
import './styles/components.css'
import './styles/auth.css'
import './styles/shell.css'
import './styles/chat.css'
import './styles/contacts.css'
import './styles/me.css'
import './styles/mobile.css'

installBridgeFallback()

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
)
