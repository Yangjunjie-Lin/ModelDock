import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { ArrowRight, Braces, KeyRound, Network, ShieldCheck } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { Form, SubmitButton } from '../components/ui'

export function LoginPage() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const navigate = useNavigate()
  const login = useMutation({ mutationFn: () => api('/auth/login', { method: 'POST', body: JSON.stringify({ email, password }) }), onSuccess: () => navigate('/') })
  return <main className="login-page console-login"><section className="login-story"><div className="login-brand"><span className="brand-mark"><Network size={20} /></span><strong>RelayDock</strong></div><div className="login-message"><div className="story-badge"><span />DEVELOPER CONSOLE</div><h1>A clean path from code to models.</h1><p>Create scoped keys, use stable aliases, and understand every request from a focused developer workspace.</p><div className="login-features"><span><Braces size={17} />OpenAI-compatible SDKs</span><span><KeyRound size={17} />Scoped RelayDock keys</span><span><ShieldCheck size={17} />Sanitized request logs</span></div></div><p className="login-foot">RelayDock Cloud Console · Authorized client access</p></section><section className="login-form-side"><div className="login-card"><div><span className="eyebrow">CONSOLE</span><h2>Welcome back</h2><p>Sign in to your RelayDock workspace.</p></div><Form className="form-stack" onSubmit={() => login.mutateAsync()}><label><span>Email address</span><input required type="email" autoComplete="username" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="you@company.com" /></label><label><span>Password</span><input required type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="Enter your password" /></label>{login.isError && <div className="form-error">{login.error instanceof Error ? login.error.message : 'Sign in failed.'}</div>}<SubmitButton pending={login.isPending}>Continue<ArrowRight size={15} /></SubmitButton></Form><p className="login-help">Need access? Contact your RelayDock administrator.</p></div></section></main>
}
