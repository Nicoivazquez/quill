import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import 'katex/dist/katex.min.css'
import 'highlight.js/styles/github-dark-dimmed.css'
import './App.css'
import App from './App.tsx'
import { ThemeProvider } from './contexts/ThemeContext'
import { BrowserRouter } from 'react-router-dom'
import { ProtectedRoute } from './components/ProtectedRoute'
import { TooltipProvider } from '@/components/ui/tooltip'
import { ToastProvider } from '@/components/ui/toast'
import { ChatEventsProvider } from './contexts/ChatEventsContext'
import { GlobalUploadProvider } from './contexts/GlobalUploadContext'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const queryClient = new QueryClient()

const isElectronRuntime =
  typeof navigator !== 'undefined' &&
  (navigator.userAgent.includes('Electron') || navigator.userAgent.includes('QuillDesktop'))

if (isElectronRuntime && 'serviceWorker' in navigator) {
  void (async () => {
    try {
      const registrations = await navigator.serviceWorker.getRegistrations()
      await Promise.all(registrations.map((registration) => registration.unregister()))
    } catch {
      // Best-effort cleanup for desktop runtime.
    }

    if ('caches' in window) {
      try {
        const cacheKeys = await caches.keys()
        await Promise.all(cacheKeys.map((key) => caches.delete(key)))
      } catch {
        // Best-effort cleanup for desktop runtime.
      }
    }
  })()
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <BrowserRouter>
          <TooltipProvider>
            <ToastProvider>
              <ChatEventsProvider>
                <ProtectedRoute>
                  <GlobalUploadProvider>
                    <App />
                  </GlobalUploadProvider>
                </ProtectedRoute>
              </ChatEventsProvider>
            </ToastProvider>
          </TooltipProvider>
        </BrowserRouter>
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>,
)
