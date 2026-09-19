import { useEffect, useState } from 'react'
import { motion } from 'framer-motion'
import { MinusIcon, SquareIcon, RestoreIcon, CloseIcon } from './icons'

/**
 * 无边框窗口的自绘标题栏。
 * 整条可拖拽；窗口控制按钮为「非拖拽区」，Windows 布局（右侧）。
 * 标题栏悬浮在内容之上，与 QQ NT 一样让内容铺满全窗。
 * 安卓 / 浏览器等直连环境（__direct）没有窗口概念，隐藏控制按钮。
 */
export function TitleBar(): JSX.Element {
  const [maximized, setMaximized] = useState(false)
  const direct = window.bridge.__direct === true

  useEffect(() => {
    if (direct) return
    return window.bridge.window.onMaximizedChanged(setMaximized)
  }, [direct])

  return (
    <div className="titlebar">
      <div className="titlebar-title">
        <span className="titlebar-logo" />
        微语
      </div>
      {!direct && (
      <div className="titlebar-controls">
        <button
          className="titlebar-btn"
          title="最小化"
          onClick={() => window.bridge.window.minimize()}
        >
          <MinusIcon width={14} height={14} />
        </button>
        <button
          className="titlebar-btn"
          title={maximized ? '还原' : '最大化'}
          onClick={() => window.bridge.window.maximize()}
        >
          {maximized ? <RestoreIcon width={12} height={12} /> : <SquareIcon width={11} height={11} />}
        </button>
        <button
          className="titlebar-btn titlebar-btn-close"
          title="关闭"
          onClick={() => window.bridge.window.close()}
        >
          <CloseIcon width={13} height={13} />
        </button>
      </div>
      )}
    </div>
  )
}

/** 标题栏左侧的呼吸光点，随连接状态变色（由 CSS var 控制） */
export function ConnectionDot({ ok }: { ok: boolean }): JSX.Element {
  return (
    <motion.span
      className={`conn-dot ${ok ? 'conn-dot-ok' : 'conn-dot-bad'}`}
      animate={{ scale: [1, 1.25, 1] }}
      transition={{ duration: 2.4, repeat: Infinity, ease: 'easeInOut' }}
    />
  )
}
