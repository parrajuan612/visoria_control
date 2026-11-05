-- Transición atómica de una entrega (SDD §3.5.1).
-- Las reglas se inyectan desde domain.Rules(): una sola fuente de verdad con Go.
--
-- KEYS[1] = vc:c:{id}:d   HASH deliveryKey -> JSON de la entrega
-- KEYS[2] = vc:c:{id}:n   HASH contadores "{canal}:{ESTADO}"
-- KEYS[3] = vc:c:{id}:ev  STREAM de eventos (SSE)
-- ARGV: 1 deliveryKey, 2 to, 3 source, 4 at(RFC3339), 5 providerID, 6 errorJSON,
--       7 detail, 8 streamMaxLen, 9 wamidPrefix, 10 wamidTTL(s), 11 msgidPrefix, 12 msgidTTL(s)
-- El ID del proveedor se indexa en la MISMA operación atómica (si el proceso muere
-- justo después de ACCEPTED, el webhook igual podrá correlacionar la entrega).
-- Devuelve {resultado, estadoPrevio}: APPLIED | IGNORED | REJECTED | NOT_FOUND
local rules = cjson.decode([==[__RULES__]==])

local key, to, source, at = ARGV[1], ARGV[2], ARGV[3], ARGV[4]
local provider_id, err_json, detail = ARGV[5], ARGV[6], ARGV[7]
local maxlen = tonumber(ARGV[8])

local raw = redis.call('HGET', KEYS[1], key)
if not raw then return {'NOT_FOUND', ''} end
local d = cjson.decode(raw)
local from, channel = d.status, d.channel

local function has(list, v)
  if not list then return false end
  for _, x in ipairs(list) do if x == v then return true end end
  return false
end

local function decide()
  if from == to then return 'IGNORED' end
  if channel == 'email' and has(rules.email_forbidden, to) then return 'REJECTED' end
  local rf, rt = rules.rank[from], rules.rank[to]
  if rf and rt then
    if rt > rf then return 'APPLIED' end
    return 'IGNORED'
  end
  if has(rules.allowed[from], to) then
    if to == 'QUEUED' and has(rules.operator_requeue_from, from) and source ~= 'operator' then
      return 'REJECTED'
    end
    return 'APPLIED'
  end
  if source == 'webhook' or source == 'imap' then return 'IGNORED' end
  return 'REJECTED'
end

local outcome = decide()
if outcome ~= 'APPLIED' then return {outcome, from} end

d.status = to
local ev = {status = to, at = at, source = source}
if detail ~= '' then ev.detail = detail end
d.history[#d.history + 1] = ev
if to == 'SENDING' then d.attempts = (d.attempts or 0) + 1 end
if provider_id ~= '' then d.provider_id = provider_id end
local perr = nil
if err_json ~= '' then
  perr = cjson.decode(err_json)
  d.last_error = perr
end
if to == 'QUEUED' then d.last_error = nil end

redis.call('HSET', KEYS[1], key, cjson.encode(d))
redis.call('HINCRBY', KEYS[2], channel .. ':' .. from, -1)
redis.call('HINCRBY', KEYS[2], channel .. ':' .. to, 1)
if provider_id ~= '' then
  if channel == 'email' then
    redis.call('SET', ARGV[11] .. provider_id, key, 'EX', tonumber(ARGV[12]))
  else
    redis.call('SET', ARGV[9] .. provider_id, key, 'EX', tonumber(ARGV[10]))
  end
end

local payload = {key = key, channel = channel, kind = d.kind, status = to,
                 previous = from, source = source, at = at}
if perr then payload.error = perr end
redis.call('XADD', KEYS[3], 'MAXLEN', '~', maxlen, '*', 'type', 'delivery', 'data', cjson.encode(payload))

return {'APPLIED', from}
