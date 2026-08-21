import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { ArrowRight, KeyRound, Network, ShieldCheck, Smartphone } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { Form, SubmitButton } from '../components/ui'
import { api } from '../lib/api'

type MFAStatus = { enabled: boolean; required: boolean }
type MFASetup = { secret: string; otpauth_uri: string; expires_at: string }

export function MFASetupPage() {
  const [code, setCode] = useState('')
  const navigate = useNavigate()
  const status = useQuery({ queryKey: ['mfa-status'], queryFn: () => api<MFAStatus>('/auth/mfa/status') })
  const setup = useMutation({ mutationFn: () => api<MFASetup>('/auth/mfa/setup', { method: 'POST' }) })
  const confirm = useMutation({ mutationFn: () => api('/auth/mfa/confirm', { method: 'POST', body: JSON.stringify({ code }) }), onSuccess: () => navigate('/') })
  return <main className="login-page"><section className="login-story"><div className="login-brand"><span className="brand-mark"><Network size={20} /></span><strong>ModelDock</strong></div><div className="login-message"><div className="story-badge"><span />ADMINISTRATOR SECURITY</div><h1>Protect control-plane access with TOTP.</h1><p>Administrator sessions can be policy-gated until an authenticator is enrolled and verified.</p><div className="login-features"><span><Smartphone size={17} />Standard authenticator support</span><span><KeyRound size={17} />Replay-protected codes</span><span><ShieldCheck size={17} />Audited enrollment</span></div></div><p className="login-foot">ModelDock Control Plane</p></section><section className="login-form-side"><div className="login-card"><div><span className="eyebrow">MULTI-FACTOR AUTHENTICATION</span><h2>{status.data?.enabled ? 'Replace authenticator' : 'Set up authenticator'}</h2><p>Generate a secret, add it to your authenticator, then confirm a six-digit code.</p></div>{!setup.data && <SubmitButton pending={setup.isPending} onClick={() => setup.mutate()} disabled={status.isLoading}>Generate setup secret<ArrowRight size={15} /></SubmitButton>}{setup.data && <Form className="form-stack" onSubmit={() => confirm.mutateAsync()}><label><span>Authenticator secret</span><input readOnly value={setup.data.secret} onFocus={(event) => event.currentTarget.select()} /></label><label><span>Six-digit code</span><input required inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" maxLength={6} value={code} onChange={(event) => setCode(event.target.value.replace(/\D/g, ''))} /></label>{confirm.isError && <div className="form-error">{confirm.error instanceof Error ? confirm.error.message : 'Verification failed.'}</div>}<SubmitButton pending={confirm.isPending}>Enable MFA<ShieldCheck size={15} /></SubmitButton></Form>}{(status.isError || setup.isError) && <div className="form-error">{String(status.error || setup.error)}</div>}</div></section></main>
}
