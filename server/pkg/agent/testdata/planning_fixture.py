#!/usr/bin/env python3
import os,sys,json,time
root=os.environ['PLAN_ROOT']; provider=os.environ['PLAN_PROVIDER']
def emit(x): print(json.dumps(x),flush=True)
def record(mode):
 p=root+'/modes'; values=json.load(open(p)) if os.path.exists(p) else []; values.append(mode);json.dump(values,open(p,'w'))
def saved(): assert os.path.exists(root+'/persisted'), 'provider interrupted before persistence'
if provider=='claude':
 args=sys.argv; mode=args[args.index('--permission-mode')+1];record(mode)
 assert '--permission-prompt-tool' in args
 msg=json.loads(sys.stdin.readline()); prompt=msg['message']['content']
 if not isinstance(prompt,str): prompt=str(prompt)
 prompt=os.environ.get('PLAN_STEP',prompt)
 emit({'type':'system','session_id':'fixture-session'})
 if 'question' in prompt:
  req={'subtype':'can_use_tool','tool_name':'AskUserQuestion','tool_use_id':'q'+str(time.time_ns()),'input':{'questions':[{'header':'Color','question':'Which color?','options':[{'label':'Blue','description':'Calm'},{'label':'Red','description':'Bright'}],'multiSelect':False}]}}
 elif 'approve' not in prompt:
  path=os.getcwd()+'/.claude/plans/plan.md';os.makedirs(os.path.dirname(path),exist_ok=True)
  body='# Revised with tests' if 'revise' in prompt else '# Blue plan'
  emit({'type':'assistant','message':{'content':[{'type':'tool_use','id':'write','name':'Write','input':{'file_path':path,'content':body}}]}})
  open(path,'w').write(body)
  emit({'type':'user','message':{'content':[{'type':'tool_result','tool_use_id':'write','content':'written'}]}})
  req={'subtype':'can_use_tool','tool_name':'ExitPlanMode','tool_use_id':'p'+str(time.time_ns()),'input':{'plan':'stale'}}
 else: req=None;assert mode=='bypassPermissions';assert '--resume' in args
 if req:
  emit({'type':'control_request','request_id':'r','request':req})
  response=json.loads(sys.stdin.readline());saved();assert response['response']['response']['behavior']=='deny'
  if 'ignore stop' in prompt: time.sleep(30)
 emit({'type':'result','subtype':'success','session_id':'fixture-session','result':'done','is_error':False})
 sys.exit(0)
thread='fixture-session'
if os.environ.get('CODEX_HOME'):
 path=os.environ['CODEX_HOME']+'/sessions/rollout-fixture-'+thread+'.jsonl';os.makedirs(os.path.dirname(path),exist_ok=True);open(path,'w').write('{}\n')
turn='turn'+str(time.time_ns())
for line in sys.stdin:
 m=json.loads(line); method=m.get('method'); ident=m.get('id'); p=m.get('params',{})
 if method=='initialize': emit({'id':ident,'result':{'userAgent':'fixture'}})
 elif method in ('thread/start','thread/resume'): emit({'id':ident,'result':{'thread':{'id':thread},'model':'fixture-model'}})
 elif method=='turn/start':
  mode=p['collaborationMode']['mode'];record(mode);prompt=os.environ.get('PLAN_STEP',str(p['input']));emit({'id':ident,'result':{'turn':{'id':turn}}})
  emit({'method':'turn/started','params':{'threadId':thread,'turn':{'id':turn}}})
  if 'question' in prompt:
   emit({'id':99,'method':'item/tool/requestUserInput','params':{'threadId':thread,'turnId':turn,'itemId':'q','isBlocking':True,'questions':[{'id':'color','header':'Color','question':'Which color?','isOther':True,'options':[{'label':'Blue','description':'Calm'}]}]}})
  else:
   if 'approve' not in prompt:
    body='# Revised with tests' if 'revise' in prompt else '# Blue plan'
    emit({'method':'item/plan/delta','params':{'threadId':thread,'turnId':turn,'itemId':'p','delta':'draft'}})
    emit({'method':'item/completed','params':{'threadId':thread,'turnId':turn,'item':{'type':'plan','id':'p','text':body}}})
   else: assert mode=='default'
   emit({'method':'turn/completed','params':{'threadId':thread,'turn':{'id':turn,'status':'completed'}}})
 elif method=='turn/interrupt':
  saved();emit({'id':ident,'result':{}})
  if 'ignore stop' in prompt: time.sleep(30)
  emit({'method':'turn/completed','params':{'threadId':thread,'turn':{'id':turn,'status':'interrupted'}}})
 elif ident is not None: emit({'id':ident,'result':{}})
