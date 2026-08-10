import { type ReactNode, useEffect, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { BellRing, Database, GitBranch, LockKeyhole, Save, Shield, SlidersHorizontal } from 'lucide-react'
import { api } from '../lib/api'
import { Button, ErrorState, Panel, Skeleton, useToast } from '../components/ui'

type Settings = {
  gateway_name?: string
  log_retention_days?: number
  audit_retention_days?: number
  log_prompt_content?: boolean
  default_rate_limit_rpm?: number
  default_rate_limit_tpm?: number
  credential_cooldown_seconds?: number
  max_scheduler_attempts?: number
  alert_high_error_rate?: number
  alert_high_429_rate?: number
  alert_pool_healthy_min?: number
}

export function SettingsPage() {
  const [tab, setTab] = useState<'general' | 'security' | 'routing' | 'alerts'>('general')
  const [form, setForm] = useState<Settings>({})
  const toast = useToast()
  const result = useQuery({ queryKey: ['settings'], queryFn: () => api<Settings>('/settings') })
  useEffect(() => { if (result.data) setForm(result.data) }, [result.data])
  const save = useMutation({ mutationFn: () => api('/settings', { method: 'PUT', body: JSON.stringify(form) }), onSuccess: () => toast('Settings saved') })

  const update = <K extends keyof Settings>(key: K, value: Settings[K]) => setForm((current) => ({ ...current, [key]: value }))

  return <div className="page-stack"><div className="page-header"><div><h1>Settings</h1><p>Control plane defaults, security posture, scheduler behavior, and alert thresholds.</p></div><Button variant="primary" disabled={save.isPending || result.isError} onClick={() => save.mutate()}><Save size={15} />{save.isPending ? 'Saving…' : 'Save changes'}</Button></div>
    {result.isLoading && <Panel><Skeleton rows={8} /></Panel>}
    {result.isError && <ErrorState error={result.error} onRetry={() => result.refetch()} />}
    {result.isSuccess && <div className="settings-layout"><nav className="settings-nav"><button className={tab === 'general' ? 'active' : ''} onClick={() => setTab('general')}><SlidersHorizontal size={16} /><span><strong>General</strong><small>Workspace and retention</small></span></button><button className={tab === 'security' ? 'active' : ''} onClick={() => setTab('security')}><LockKeyhole size={16} /><span><strong>Security</strong><small>Logging and secret policy</small></span></button><button className={tab === 'routing' ? 'active' : ''} onClick={() => setTab('routing')}><GitBranch size={16} /><span><strong>Routing</strong><small>Scheduler defaults</small></span></button><button className={tab === 'alerts' ? 'active' : ''} onClick={() => setTab('alerts')}><BellRing size={16} /><span><strong>Alerts</strong><small>Operational thresholds</small></span></button></nav><div className="settings-content">
      {tab === 'general' && <Panel title="General settings" description="Workspace identity and data retention."><div className="settings-form"><Setting label="Gateway name" hint="Displayed in operator-facing interfaces."><input value={form.gateway_name || ''} onChange={(event) => update('gateway_name', event.target.value)} /></Setting><Setting label="Request log retention" hint="Sanitized request metadata, in days."><input type="number" min="1" value={form.log_retention_days || ''} onChange={(event) => update('log_retention_days', Number(event.target.value))} /></Setting><Setting label="Audit log retention" hint="Administrative audit history, in days."><input type="number" min="30" value={form.audit_retention_days || ''} onChange={(event) => update('audit_retention_days', Number(event.target.value))} /></Setting></div></Panel>}
      {tab === 'security' && <Panel title="Security controls" description="RelayDock never exposes upstream credential plaintext."><div className="security-card"><Shield size={20} /><div><strong>Provider secrets encrypted at rest</strong><p>Credential APIs return only whether a secret exists and its final four characters.</p></div><span>Required</span></div><div className="security-card"><Database size={20} /><div><strong>Prompt content logging</strong><p>Keep disabled unless an approved policy and retention process is in place.</p></div><label className="switch"><input type="checkbox" checked={Boolean(form.log_prompt_content)} onChange={(event) => update('log_prompt_content', event.target.checked)} /><i /></label></div>{form.log_prompt_content && <div className="inline-warning">Prompt content may contain sensitive user data. Review your privacy and retention policy before saving.</div>}</Panel>}
      {tab === 'routing' && <Panel title="Scheduler defaults" description="Used when a route or key does not provide a more specific limit."><div className="settings-form"><Setting label="Default RPM" hint="Requests allowed per minute."><input type="number" value={form.default_rate_limit_rpm || ''} onChange={(event) => update('default_rate_limit_rpm', Number(event.target.value))} /></Setting><Setting label="Default TPM" hint="Tokens allowed per minute."><input type="number" value={form.default_rate_limit_tpm || ''} onChange={(event) => update('default_rate_limit_tpm', Number(event.target.value))} /></Setting><Setting label="Credential cooldown" hint="Seconds excluded after a retryable upstream response."><input type="number" min="1" value={form.credential_cooldown_seconds || ''} onChange={(event) => update('credential_cooldown_seconds', Number(event.target.value))} /></Setting><Setting label="Max scheduler attempts" hint="Bounded attempts across eligible credentials."><input type="number" min="1" max="5" value={form.max_scheduler_attempts || ''} onChange={(event) => update('max_scheduler_attempts', Number(event.target.value))} /></Setting></div></Panel>}
      {tab === 'alerts' && <Panel title="Alert thresholds" description="Conditions appear in the Admin dashboard and Alerts page."><div className="settings-form"><Setting label="High error rate" hint="Percent over the evaluation window."><input type="number" min="0" max="100" value={form.alert_high_error_rate || ''} onChange={(event) => update('alert_high_error_rate', Number(event.target.value))} /></Setting><Setting label="High 429 rate" hint="Percent of requests rate limited."><input type="number" min="0" max="100" value={form.alert_high_429_rate || ''} onChange={(event) => update('alert_high_429_rate', Number(event.target.value))} /></Setting><Setting label="Minimum healthy pool size" hint="Alert below this eligible credential count."><input type="number" min="1" value={form.alert_pool_healthy_min || ''} onChange={(event) => update('alert_pool_healthy_min', Number(event.target.value))} /></Setting></div></Panel>}
      {save.isError && <div className="form-error">{save.error instanceof Error ? save.error.message : 'Unable to save settings.'}</div>}
    </div></div>}
  </div>
}

function Setting({ label, hint, children }: { label: string; hint: string; children: ReactNode }) {
  return <label className="setting-row"><span><strong>{label}</strong><small>{hint}</small></span>{children}</label>
}
