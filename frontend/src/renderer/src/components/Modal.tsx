import { useId, useLayoutEffect, useRef, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { AnimatePresence, motion } from 'framer-motion'
import { CloseIcon } from './icons'

interface ModalProps {
  open: boolean
  title: string
  width?: number
  onClose: () => void
  children: ReactNode
}

/**
 * 居中模态框：遮罩淡入 + 卡片弹性放大，退出时反向收场。
 * 承载「加好友 / 建群 / 服务器设置 / 退出群聊」等轻操作。
 */
export function Modal({ open, title, width = 420, onClose, children }: ModalProps): JSX.Element {
  const titleId = useId()
  const overlayRef = useRef<HTMLDivElement>(null)
  const cardRef = useRef<HTMLDivElement>(null)
  const closeRef = useRef(onClose)
  closeRef.current = onClose

  useLayoutEffect(() => {
    if (!open || !cardRef.current || !overlayRef.current) return
    const card = cardRef.current
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    // 遮罩不只是挡住鼠标；背景控件也不能继续被 Tab 或屏幕阅读器操作。
    const background = [...document.body.children]
      .filter((el): el is HTMLElement => el instanceof HTMLElement && el !== overlayRef.current)
      .map((el) => ({ el, inert: el.inert }))
    background.forEach(({ el }) => { el.inert = true })
    const focusable = (): HTMLElement[] => [...card.querySelectorAll<HTMLElement>(
      'button, input, textarea, select, a[href], [tabindex]'
    )].filter((el) => el.tabIndex >= 0 && !el.matches(':disabled') && el.getClientRects().length > 0)
    const firstInput = card.querySelector<HTMLElement>('input:not(:disabled), textarea:not(:disabled), select:not(:disabled)')
    ;(firstInput ?? focusable()[0] ?? card).focus()
    const onKeyDown = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        closeRef.current()
      } else if (e.key === 'Tab') {
        const items = focusable()
        const first = items[0] ?? card
        const last = items[items.length - 1] ?? card
        if (!card.contains(document.activeElement) || items.length === 0 ||
          (e.shiftKey ? document.activeElement === first : document.activeElement === last)) {
          e.preventDefault()
          ;(e.shiftKey ? last : first).focus()
        }
      }
    }
    document.addEventListener('keydown', onKeyDown, true)
    return () => {
      document.removeEventListener('keydown', onKeyDown, true)
      background.forEach(({ el, inert }) => { el.inert = inert })
      if (previousFocus?.isConnected) previousFocus.focus()
    }
  }, [open])

  // 浮层必须相对窗口定位，不能被父级动画 transform 或面板 overflow 限制。
  return createPortal(
    <AnimatePresence>
      {open && (
        <motion.div
          ref={overlayRef}
          className="modal-overlay"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          transition={{ duration: 0.18 }}
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) onClose()
          }}
        >
          <motion.div
            ref={cardRef}
            role="dialog"
            aria-modal="true"
            aria-labelledby={titleId}
            tabIndex={-1}
            className="modal-card"
            style={{ width }}
            initial={{ opacity: 0, scale: 0.92, y: 16 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.95, y: 10 }}
            transition={{ type: 'spring', stiffness: 380, damping: 30 }}
          >
            <header className="modal-header">
              <h3 id={titleId}>{title}</h3>
              <button className="icon-btn" onClick={onClose} title="关闭">
                <CloseIcon width={16} height={16} />
              </button>
            </header>
            <div className="modal-body">{children}</div>
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>,
    document.body
  )
}
