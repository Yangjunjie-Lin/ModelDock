import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Braces, Copy, KeyRound, LoaderCircle, Play, RotateCcw, ShieldCheck, Square } from 'lucide-react'
import { api, asPage } from '../lib/api'
import { Badge, Button, ErrorState, Panel, useToast } from '../components/ui'
import { consoleV2Paths, useProjectScope } from '../lib/project-scope'

type Model = Record<string, unknown>
const gatewayBase = '/v1'

export function PlaygroundPage() {
  const scope = useProjectScope()
  const [endpoint, setEndpoint] = useState<'responses' | 'chat'>('responses')
  const [model, setModel] = useState('gpt-default')
  const [apiKey, setApiKey] = useState('')
  const [system, setSystem] = useState('You are a concise, helpful assistant.')
  const [input, setInput] = useState('Explain why stable model aliases are useful in an API gateway.')
  const [stream, setStream] = useState(true)
  const [running, setRunning] = useState(false)
  const [output, setOutput] = useState('')
  const [error, setError] = useState('')
  const [meta, setMeta] = useState<{ status?: number; elapsed?: number }>({})
  const [controller, setController] = useState<AbortController | null>(null)
  const toast = useToast()
  const models = useQuery({ queryKey: ['playground-models', scope.projectID], queryFn: () => api<unknown>(consoleV2Paths.projectModels(scope.projectID), { query: { project_id: scope.projectID, enabled: true } }).then(asPage<Model>), enabled: Boolean(scope.projectID) })
  const aliases = useMemo(() => models.data?.items.map((item) => String(item.alias || item.id_alias || item.id || item.name)).filter(Boolean) || [], [models.data])
  useEffect(() => { if (aliases.length && !aliases.includes(model)) setModel(aliases[0]) }, [aliases, model])

  const run = async () => {
    if (!apiKey.trim()) { setError('Enter a RelayDock API key. It is kept only in this browser tab and is never saved by Console.'); return }
    if (!input.trim()) { setError('Enter a prompt before running the request.'); return }
    const abort = new AbortController(); setController(abort); setRunning(true); setOutput(''); setError(''); const started = performance.now()
    try {
      const body = endpoint === 'responses'
        ? { model, instructions: system || undefined, input, stream }
        : { model, messages: [...(system ? [{ role: 'system', content: system }] : []), { role: 'user', content: input }], stream }
      const response = await fetch(`${gatewayBase}/${endpoint === 'responses' ? 'responses' : 'chat/completions'}`, { method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${apiKey.trim()}` }, body: JSON.stringify(body), signal: abort.signal })
      setMeta({ status: response.status, elapsed: Math.round(performance.now() - started) })
      if (!response.ok) { const value = await response.json().catch(() => null); throw new Error(value?.error?.message || value?.message || `Request failed (${response.status})`) }
      if (stream && response.body) {
        const reader = response.body.getReader(); const decoder = new TextDecoder(); let buffer = ''
        while (true) {
          const { value, done } = await reader.read(); if (done) break
          buffer += decoder.decode(value, { stream: true }); const lines = buffer.split('\n'); buffer = lines.pop() || ''
          for (const line of lines) { if (!line.startsWith('data:')) continue; const data = line.slice(5).trim(); if (!data || data === '[DONE]') continue; try { const event = JSON.parse(data); const delta = event.delta || event.choices?.[0]?.delta?.content || event.output_text || ''; if (typeof delta === 'string') setOutput((current) => current + delta) } catch { /* ignore SSE comments and incomplete events */ } }
        }
      } else {
        const value = await response.json(); const text = value.output_text || value.choices?.[0]?.message?.content || JSON.stringify(value, null, 2); setOutput(String(text))
      }
    } catch (reason) {
      if ((reason as Error).name !== 'AbortError') setError(reason instanceof Error ? reason.message : 'Playground request failed.')
    } finally { setRunning(false); setController(null); setMeta((current) => ({ ...current, elapsed: Math.round(performance.now() - started) })) }
  }

  return <div className="page-stack playground-page"><div className="page-header"><div><h1>Playground</h1><p>Test RelayDock model aliases with an OpenAI-compatible request.</p></div><div className="header-actions"><Badge tone="info">Client-side preview</Badge><Button onClick={() => { setOutput(''); setError(''); setMeta({}) }}><RotateCcw size={14} />Reset output</Button></div></div>
    <div className="playground-security"><ShieldCheck size={16} /><span>The API key exists only in component memory and is cleared when this page closes. Console does not persist it.</span></div>
    {models.isError && <ErrorState error={models.error} onRetry={() => models.refetch()} />}
    <div className="playground-grid"><Panel className="playground-editor"><div className="playground-config"><label><span>Endpoint</span><select value={endpoint} onChange={(event) => setEndpoint(event.target.value as 'responses' | 'chat')}><option value="responses">Responses API</option><option value="chat">Chat Completions</option></select></label><label><span>Model</span>{aliases.length ? <select value={model} onChange={(event) => setModel(event.target.value)}>{aliases.map((alias) => <option key={alias} value={alias}>{alias}</option>)}</select> : <input value={model} onChange={(event) => setModel(event.target.value)} placeholder="Model alias" />}</label><label className="playground-key"><span>RelayDock API key</span><div><KeyRound size={14} /><input type="password" autoComplete="off" value={apiKey} onChange={(event) => setApiKey(event.target.value)} placeholder="rdk_live_…" /></div></label><label className="stream-toggle"><span><strong>Stream response</strong><small>Render server-sent events as they arrive</small></span><span className="switch"><input type="checkbox" checked={stream} onChange={(event) => setStream(event.target.checked)} /><i /></span></label></div><div className="prompt-editor"><label><span>System instructions</span><textarea value={system} onChange={(event) => setSystem(event.target.value)} rows={3} /></label><label><span>Input</span><textarea value={input} onChange={(event) => setInput(event.target.value)} rows={9} /></label></div><div className="playground-run">{running ? <Button variant="danger" onClick={() => controller?.abort()}><Square size={13} />Stop</Button> : <Button variant="primary" onClick={() => void run()}><Play size={14} />Run request</Button>}</div></Panel><Panel className="playground-output" title="Response" description={meta.status ? `HTTP ${meta.status} · ${meta.elapsed || 0} ms` : 'Output will appear here'} action={output && <Button size="sm" onClick={() => { void navigator.clipboard.writeText(output); toast('Response copied') }}><Copy size={13} />Copy</Button>}>{running && !output && <div className="output-loading"><LoaderCircle className="spin" size={18} />Waiting for the first response chunk…</div>}{error && <div className="form-error">{error}</div>}{output ? <pre className="output-content"><code>{output}</code></pre> : !running && !error && <div className="output-empty"><Braces size={25} /><strong>No response yet</strong><span>Configure the request and press Run.</span></div>}</Panel></div>
  </div>
}
