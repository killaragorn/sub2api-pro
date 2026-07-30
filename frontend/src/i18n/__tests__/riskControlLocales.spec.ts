import { describe, expect, it } from 'vitest'

import en from '../locales/en'
import zh from '../locales/zh'

describe('risk control locale copy', () => {
  it.each([
    ['zh', zh],
    ['en', en]
  ] as const)('%s includes action filter and detail copy messages', (_locale, messages) => {
    const riskControl = messages.admin.riskControl
    expect(riskControl.actionFilter).toMatchObject({
      all: expect.any(String),
      keywordBlock: expect.any(String),
      block: expect.any(String),
      cyberPolicy: expect.any(String),
      hashBlock: expect.any(String),
    })
    expect(riskControl.action.hashBlock).toEqual(expect.any(String))
    expect(riskControl.detailCopy).toEqual(expect.any(String))
    expect(riskControl.detailCopied).toEqual(expect.any(String))
  })

  it('describes worker runtime as audit and pre-block record processing', () => {
    expect(zh.admin.riskControl.workerStatusHint).toContain('前置拦截记录任务')
    expect(zh.admin.riskControl.workerStatusHint).not.toContain('异步观察任务')
    expect(en.admin.riskControl.workerStatusHint).toContain('pre-block record tasks')
    expect(en.admin.riskControl.workerStatusHint).not.toContain('observation tasks')
  })

  it('keeps pre-block audit key summary aware of async worker load', () => {
    expect(zh.admin.riskControl.preBlockAPIKeyLoadSummary).toContain('worker：{workerActive} / {workerTotal}')
    expect(en.admin.riskControl.preBlockAPIKeyLoadSummary).toContain('worker: {workerActive} / {workerTotal}')
  })

  it('does not describe pre-block audit key polling as bypassing the worker pool', () => {
    expect(zh.admin.riskControl.preBlockAPIKeyLoadHint).toBe('同步前置拦截直接轮询可用审核 Key。')
    expect(zh.admin.riskControl.preBlockAPIKeyLoadHint).not.toContain('Worker 池')
    expect(en.admin.riskControl.preBlockAPIKeyLoadHint).not.toContain('worker pool')
  })
})
