import assert from 'node:assert/strict'
import test from 'node:test'

import {
  compareSemVer,
  formatBytes,
  isAgentOutdated,
  parseSemVer,
  runtimeLabel,
  statusLabel,
  taskStatusLabel,
} from '../.test-dist/nodeDisplay.js'

test('formatBytes handles boundaries and invalid telemetry', () => {
  assert.equal(formatBytes(), '—')
  assert.equal(formatBytes(0), '0 B')
  assert.equal(formatBytes(1024), '1.0 KB')
  assert.equal(formatBytes(5 * 1024 * 1024), '5.0 MB')
  assert.equal(formatBytes(Number.NaN), '0 B')
})

test('semver parser finds version tokens and compares prereleases', () => {
  assert.deepEqual(parseSemVer('agent v1.2.3'), {
    major: 1,
    minor: 2,
    patch: 3,
    pre: '',
    valid: true,
    raw: 'agent v1.2.3',
  })
  assert.equal(compareSemVer(parseSemVer('1.2.3-rc.1'), parseSemVer('1.2.3')), -1)
  assert.equal(compareSemVer(parseSemVer('1.10.0'), parseSemVer('1.9.9')), 1)
})

test('agent upgrade detection does not downgrade newer versions', () => {
  assert.equal(isAgentOutdated('v0.3.0', 'v0.4.0'), true)
  assert.equal(isAgentOutdated('v0.5.0', 'v0.4.0'), false)
  assert.equal(isAgentOutdated('unknown', 'v0.4.0'), true)
  assert.equal(isAgentOutdated('', ''), false)
})

test('status helpers expose stable Chinese labels', () => {
  assert.equal(statusLabel('unauthorized'), '鉴权失败')
  assert.equal(runtimeLabel('running'), '运行中')
  assert.equal(taskStatusLabel('partial'), '部分成功')
})
