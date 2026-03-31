import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type PropsWithChildren } from 'react'

type ToastVariant = 'default' | 'warning' | 'error'

type Toast = {
  id: number
  title: string
  description?: string
  variant?: ToastVariant
}

type ToastContextValue = {
  toast: (t: Omit<Toast, 'id'>) => void
}

const ToastContext = createContext<ToastContextValue | null>(null)

const variantClasses: Record<ToastVariant, string> = {
  default: 'bg-carbon-900 text-white ring-black/10 dark:bg-carbon-800 dark:text-carbon-100',
  warning: 'bg-amber-900 text-amber-50 ring-amber-700/30 dark:bg-amber-950 dark:text-amber-100',
  error: 'bg-red-900 text-red-50 ring-red-700/30 dark:bg-red-950 dark:text-red-100',
}

const AUTO_DISMISS_MS: Record<ToastVariant, number> = {
  default: 2600,
  warning: 8000,
  error: 8000,
}

export function ToastProvider({ children }: PropsWithChildren) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const timersRef = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map())

  // Clean up all timers on unmount.
  useEffect(() => {
    return () => {
      timersRef.current.forEach(handle => clearTimeout(handle))
      timersRef.current.clear()
    }
  }, [])

  const toast = useCallback((t: Omit<Toast, 'id'>) => {
    const id = Date.now() + Math.random()
    const toastItem: Toast = { id, ...t }
    setToasts(prev => [...prev, toastItem])
    const handle = setTimeout(() => {
      setToasts(prev => prev.filter(x => x.id !== id))
      timersRef.current.delete(id)
    }, AUTO_DISMISS_MS[t.variant ?? 'default'])
    timersRef.current.set(id, handle)
  }, [])

  const value = useMemo(() => ({ toast }), [toast])

  return (
    <ToastContext.Provider value={value}>
      {children}
      {/* Toaster container */}
      <div className="pointer-events-none fixed bottom-4 right-4 z-[60] flex flex-col gap-2">
        {toasts.map(t => (
          <div
            key={t.id}
            className={`pointer-events-auto min-w-[220px] max-w-[360px] rounded-md shadow-lg ring-1 transition-all ${variantClasses[t.variant ?? 'default']}`}
          >
            <div className="px-3 py-2">
              <div className="text-sm font-medium">{t.title}</div>
              {t.description && (
                <div className="text-xs opacity-75 mt-0.5">{t.description}</div>
              )}
            </div>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export function useToast() {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast must be used within ToastProvider')
  return ctx
}
