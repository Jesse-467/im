const { test } = require('node:test')
const assert = require('node:assert/strict')
const { mergeMessages, receivedCursor, toLocalMsg } = require('../out/tests/core/message-state.js')

const msg = (seq, overrides = {}) => ({
  id: String(90071992547409910n + BigInt(seq)), conversationId: '1', groupId: '1',
  seq, senderId: '67', type: 1, content: `message-${seq}`, uuid: `uuid-${seq}`, createTime: seq,
  ...overrides
})

test('120 条补齐消息逐页推进，不跳过第二页', () => {
  let cursor = 0
  let list = []
  for (const [start, end] of [[1, 50], [51, 100], [101, 120]]) {
    const page = Array.from({ length: end - start + 1 }, (_, i) => msg(start + i))
    cursor = receivedCursor(cursor, page)
    list = mergeMessages(list, page)
    assert.equal(cursor, end)
  }
  assert.equal(list.length, 120)
  assert.deepEqual(list.map((m) => m.seq), Array.from({ length: 120 }, (_, i) => i + 1))
})

test('向前翻页合并保序且重复拉取不增加消息', () => {
  const latest = Array.from({ length: 50 }, (_, i) => toLocalMsg(msg(i + 71)))
  const older = Array.from({ length: 50 }, (_, i) => msg(70 - i))
  const merged = mergeMessages(latest, older)
  assert.equal(merged.length, 100)
  assert.equal(merged[0].seq, 21)
  assert.equal(merged[99].seq, 120)
  assert.deepEqual(mergeMessages(merged, older), merged)
})

test('发送回执项不会遮住较早的他人消息，pending 保留在尾部', () => {
  const acknowledged = toLocalMsg(msg(3))
  const pending = { ...toLocalMsg(msg(0, { id: '', uuid: 'pending' })), pending: true }
  const merged = mergeMessages([toLocalMsg(msg(1)), acknowledged, pending], [msg(2), msg(3)])
  assert.deepEqual(merged.map((m) => m.seq), [1, 2, 3, 0])
  assert.equal(merged.length, 4)
  assert.equal(receivedCursor(1, [msg(2)]), 2)
})

test('幂等重发将乐观项确认，不重复展示，雪花 ID 原样保留', () => {
  const remote = msg(1)
  const optimistic = { ...toLocalMsg(remote), id: '', seq: 0, pending: true }
  const merged = mergeMessages([optimistic], [remote])
  assert.equal(merged.length, 1)
  assert.equal(merged[0].id, '90071992547409911')
  assert.equal(merged[0].pending, false)
})
