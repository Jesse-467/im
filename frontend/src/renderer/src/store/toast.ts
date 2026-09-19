import { create } from 'zustand'

export type ToastType = 'info' | 'success' | 'error'

export interface ToastItem {
  id: number
  type: ToastType
  text: string
}

interface ToastState {
  toasts: ToastItem[]
  push: (text: string, type?: ToastType) => void
  dismiss: (id: number) => void
}

let nextId = 1

export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  push: (text, type = 'info') => {
    const id = nextId++
    set((s) => ({ toasts: [...s.toasts, { id, type, text }] }))
    setTimeout(() => {
      set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) }))
    }, 2600)
  },
  dismiss: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) }))
}))

export const toast = (text: string, type: ToastType = 'info'): void => {
  useToastStore.getState().push(text, type)
}
