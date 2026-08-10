import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { ArrowRight, KeyRound, LockKeyhole, Network, ShieldCheck } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { Button, Form, SubmitButton } from '../components/ui'
import { LanguageToggle } from '../lib/i18n'

export function LoginPage() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const navigate = useNavigate()
  const login = useMutation({
    mutationFn: () => api('/auth/login', { method: 'POST', body: JSON.stringify({ email, password }) }),
    onSuccess: () => navigate('/'),
  })

  return (
    <main className="login-page"><div className="login-language"><LanguageToggle /></div>
      <section className="login-story">
        <div className="login-brand"><span className="brand-mark"><Network size={20} /></span><strong>RelayDock</strong></div>
        <div className="login-message"><BadgeLine /><h1>Operate every model route from one control plane.</h1><p>Monitor authorized provider credentials, routing health, usage, and access policy without exposing upstream secrets.</p><div className="login-features"><span><ShieldCheck size={17} />Encrypted provider credentials</span><span><KeyRound size={17} />Hashed downstream API keys</span><span><LockKeyhole size={17} />Audited administrative actions</span></div></div>
        <p className="login-foot">RelayDock Control Plane · Authorized provider API credentials only</p>
      </section>
      <section className="login-form-side">
        <div className="login-card">
          <div><span className="eyebrow">ADMINISTRATION</span><h2>Sign in to RelayDock</h2><p>Use your administrator account to continue.</p></div>
          <Form className="form-stack" onSubmit={() => login.mutateAsync()}>
            <label><span>Email address</span><input required type="email" autoComplete="username" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="admin@company.com" /></label>
            <label><span>Password</span><input required type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="Enter your password" /></label>
            {login.isError && <div className="form-error">{login.error instanceof Error ? login.error.message : 'Sign in failed.'}</div>}
            <SubmitButton pending={login.isPending}>Continue<ArrowRight size={15} /></SubmitButton>
          </Form>
          <p className="login-help">Access is managed by your RelayDock administrator.</p>
        </div>
      </section>
    </main>
  )
}

function BadgeLine() {
  return <div className="story-badge"><span />CONTROL PLANE ONLINE</div>
}
