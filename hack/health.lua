-- Runs one health check the way Argo CD does: the check is a chunk of Lua
-- that sees the live object as the global `obj` and returns the health
-- table. Argo CD embeds gopher-lua (Lua 5.1), so this runs under 5.1.
--
--   lua hack/health.lua <check.lua> <object.json>
--
-- prints "<status><TAB><message>". JSON null becomes nil, which is what
-- Argo CD's own conversion does with it.

local function decode(s)
  local pos = 1
  local function ws() pos = s:find("[^ \t\r\n]", pos) or #s + 1 end
  local value
  local function str()
    local out, i = {}, pos + 1
    while true do
      local c = s:sub(i, i)
      if c == '"' then pos = i + 1; return table.concat(out) end
      if c == "\\" then
        local n = s:sub(i + 1, i + 1)
        local map = { n = "\n", t = "\t", r = "\r", b = "\b", f = "\f", ["/"] = "/", ["\\"] = "\\", ['"'] = '"' }
        if n == "u" then
          local cp = tonumber(s:sub(i + 2, i + 5), 16)
          out[#out + 1] = cp < 128 and string.char(cp) or "?"
          i = i + 6
        else
          out[#out + 1] = map[n]
          i = i + 2
        end
      else
        out[#out + 1] = c
        i = i + 1
      end
    end
  end
  function value()
    ws()
    local c = s:sub(pos, pos)
    if c == "{" then
      local t = {}
      pos = pos + 1; ws()
      if s:sub(pos, pos) == "}" then pos = pos + 1; return t end
      while true do
        ws()
        local k = str(); ws()
        pos = pos + 1 -- the colon
        t[k] = value(); ws()
        local d = s:sub(pos, pos); pos = pos + 1
        if d == "}" then return t end
      end
    elseif c == "[" then
      local t, n = {}, 0
      pos = pos + 1; ws()
      if s:sub(pos, pos) == "]" then pos = pos + 1; return t end
      while true do
        n = n + 1
        t[n] = value(); ws()
        local d = s:sub(pos, pos); pos = pos + 1
        if d == "]" then return t end
      end
    elseif c == '"' then
      return str()
    elseif s:sub(pos, pos + 3) == "true" then pos = pos + 4; return true
    elseif s:sub(pos, pos + 4) == "false" then pos = pos + 5; return false
    elseif s:sub(pos, pos + 3) == "null" then pos = pos + 4; return nil
    else
      local num = s:match("^-?[%d.eE+-]+", pos)
      pos = pos + #num
      return tonumber(num)
    end
  end
  return value()
end

local function slurp(path)
  local f = assert(io.open(path, "rb"))
  local s = f:read("*a")
  f:close()
  return s
end

local chunk, err = loadstring(slurp(arg[1]), "check")
if not chunk then io.stderr:write("lua: " .. err .. "\n"); os.exit(2) end

-- What gopher-lua's sandbox exposes to a health check, and no more.
setfenv(chunk, {
  obj = decode(slurp(arg[2])),
  ipairs = ipairs, pairs = pairs, tostring = tostring, tonumber = tonumber,
  type = type, string = string, table = table, math = math,
})

local hs = chunk()
if type(hs) ~= "table" or hs.status == nil then
  io.stderr:write("the check returned no health table with a status\n")
  os.exit(2)
end
print(hs.status .. "\t" .. tostring(hs.message or ""))
