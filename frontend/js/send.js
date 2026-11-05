// Paso 3: generar las propuestas (PDF), enviarlas por WhatsApp y correo, y seguir el estado REAL de cada
// entrega. El estado viene del servidor (se consulta cada pocos segundos); aquí no se deduce nada del texto.
// "Aceptado" significa que el proveedor recibió el mensaje, no que el acudiente lo recibió: se dice así.
// Todo se pinta con nodos DOM/textContent (nunca innerHTML).
(function () {
    const POLL_MS = 2000;
    const st = { id: null, campaign: null, recipients: [], snap: null, timer: null, busy: false, filter: 'all', msg: null };

    function node(tag, cls, text) {
        const n = document.createElement(tag);
        if (cls) n.className = cls;
        if (text !== undefined) n.textContent = text;
        return n;
    }
    const btn = (label, cls, onClick) => {
        const b = node('button', `font-bold py-2.5 px-5 rounded-lg text-sm cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed ${cls}`, label);
        b.type = 'button';
        b.addEventListener('click', onClick);
        return b;
    };

    // Colores por estado (los textos vienen del servidor: «Aceptado por WhatsApp», «No entregado»…)
    const STATUS_CLASS = {
        ACCEPTED: 'text-green-700', SENT: 'text-green-700', DELIVERED: 'text-green-700', READ: 'text-green-700',
        FAILED: 'text-red-600', NEEDS_REVIEW: 'text-amber-700', UNCONFIRMED: 'text-amber-700', RETRY_WAIT: 'text-amber-700',
        SENDING: 'text-blue-600', QUEUED: 'text-gray-500', PENDING: 'text-gray-500', SKIPPED: 'text-gray-400',
    };
    const SEGMENTS = [
        { states: ['ACCEPTED', 'SENT', 'DELIVERED', 'READ'], cls: 'bg-green-500', name: 'aceptadas' },
        { states: ['SENDING', 'QUEUED', 'PENDING'], cls: 'bg-blue-300', name: 'en cola' },
        { states: ['RETRY_WAIT', 'NEEDS_REVIEW', 'UNCONFIRMED'], cls: 'bg-amber-400', name: 'por revisar' },
        { states: ['FAILED'], cls: 'bg-red-500', name: 'fallidas' },
        { states: ['SKIPPED'], cls: 'bg-gray-300', name: 'omitidas' },
    ];
    const CHANNEL_NAME = { whatsapp: 'WhatsApp', email: 'Correo' };

    const status = () => (st.snap && st.snap.status) || (st.campaign && st.campaign.status) || '';
    const validRecipients = () => st.recipients.filter(r => r.estado === 'VALID');
    const withDoc = () => validRecipients().filter(r => r.documento && r.documento.path && !r.documento.error);
    const docErrors = () => validRecipients().filter(r => r.documento && r.documento.error);
    const running = () => ['DOCS_RUNNING', 'DISPATCHING'].includes(status());

    // ------------------------------------------------------------ carga y sondeo
    async function refresh() {
        if (!st.id) return;
        const c = await getCampaign(st.id);
        if (!c.ok) { st.msg = { kind: 'error', text: c.data.error || 'No se pudo leer la campaña.' }; render(); return; }
        st.campaign = c.data.campaign;
        st.recipients = c.data.recipients;
        if (['DISPATCHING', 'PAUSED', 'COMPLETED'].includes(st.campaign.status)) {
            const d = await getDeliveries(st.id);
            if (d.ok) st.snap = d.data;
        }
        render();
        if (running()) schedule(); else stop();
    }
    function schedule() { if (!st.timer) st.timer = setInterval(() => refresh().catch(() => {}), POLL_MS); }
    function stop() { if (st.timer) { clearInterval(st.timer); st.timer = null; } }

    async function act(fn, okMsg) {
        if (st.busy) return;
        st.busy = true; st.msg = null; render();
        try {
            const r = await fn();
            // Los avisos de éxito caducan (el estado real ya se ve en las tarjetas); los errores se quedan.
            st.msg = r.ok ? { kind: 'ok', text: okMsg(r.data), until: Date.now() + 6000 } : { kind: 'error', text: r.data.error || 'No se pudo completar la acción.' };
            if (r.ok) setTimeout(render, 6100);
        } catch (e) {
            st.msg = { kind: 'error', text: 'No se pudo conectar con el servidor.' };
        }
        st.busy = false;
        await refresh();
    }

    // ------------------------------------------------------------ acciones
    const doGenerate = () => act(() => generateDocuments(st.id), d => d.message || 'Generando las propuestas…');
    function doSend() {
        const chans = ['whatsapp', 'email'].filter(c => document.getElementById('chk_' + c) && document.getElementById('chk_' + c).checked);
        if (!chans.length) { st.msg = { kind: 'error', text: 'Elige al menos un canal.' }; render(); return; }
        const n = withDoc().length;
        const names = chans.map(c => CHANNEL_NAME[c]).join(' y ');
        if (!confirm(`Se enviarán ${n} propuesta(s) por ${names} a los acudiantes de verdad.\n\n¿Continuar?`)) return;
        act(() => dispatchCampaign(st.id, chans), d => {
            const r = d.resultado || {};
            return `Envío en curso: ${r.en_cola} mensaje(s) en cola` + (r.omitidas ? `, ${r.omitidas} omitido(s) por falta de dato` : '') + '.';
        });
    }
    const doRetry = () => act(() => retryCampaign(st.id), d => {
        const r = d.resultado || {};
        return r.reencoladas ? `Se reenviaron ${r.reencoladas} entrega(s).` + (r.fallos_permanentes ? ` ${r.fallos_permanentes} no se reintentan (el dato está mal).` : '')
            : (r.fallos_permanentes ? `Nada que reenviar: ${r.fallos_permanentes} fallo(s) son permanentes (corrige el dato).` : 'No hay entregas por reenviar.');
    });
    const doResume = () => act(() => resumeCampaign(st.id), d => d.message || 'Campaña reanudada.');

    // ------------------------------------------------------------ pintado
    function card(title, children) {
        const c = node('div', 'bg-white border border-gray-200 rounded-xl p-5 shadow-sm');
        c.append(node('h3', 'font-bold text-gray-700 mb-3', title), ...children);
        return c;
    }
    function banner(kind, text) {
        const cls = { ok: 'bg-green-50 border-green-200 text-green-800', error: 'bg-red-50 border-red-200 text-red-800', warn: 'bg-amber-50 border-amber-200 text-amber-800', info: 'bg-blue-50 border-blue-200 text-blue-800' }[kind];
        return node('div', `border rounded-lg p-3 text-sm font-medium ${cls}`, text);
    }
    function bar(parts, total) {
        const wrap = node('div', 'w-full bg-gray-100 rounded-full h-3 overflow-hidden flex');
        parts.forEach(p => {
            if (!p.n || !total) return;
            const seg = node('div', `${p.cls} h-3 transition-all duration-500`);
            seg.style.width = `${(p.n / total) * 100}%`;
            seg.title = `${p.n} ${p.name}`;
            wrap.append(seg);
        });
        return wrap;
    }

    function docsCard() {
        const valid = validRecipients(), done = withDoc(), bad = docErrors();
        const parts = [];
        const gen = status() === 'DOCS_RUNNING';
        const ready = valid.length > 0 && done.length + bad.length === valid.length && !gen;
        parts.push(node('p', 'text-sm text-gray-600 mb-3',
            `${valid.length} fila(s) válidas. Cada propuesta se genera desde las plantillas Word del cliente y se convierte a PDF.`));
        parts.push(bar([{ n: done.length, cls: 'bg-green-500', name: 'generadas' }, { n: bad.length, cls: 'bg-red-500', name: 'con error' }], valid.length));
        parts.push(node('p', 'text-sm mt-2 mb-3 font-medium',
            gen ? `Generando… ${done.length + bad.length} de ${valid.length}` : `${done.length} generada(s)` + (bad.length ? `, ${bad.length} con error` : '') + ` de ${valid.length}`));
        const pending = valid.length - done.length - bad.length;
        const b = btn(pending > 0 || bad.length === 0 && done.length === 0 ? 'Generar propuestas' : (bad.length ? 'Volver a intentar las que fallaron' : 'Propuestas generadas ✓'),
            'bg-blue-600 hover:bg-blue-700 text-white', doGenerate);
        b.disabled = st.busy || gen || ['DISPATCHING', 'COMPLETED', 'PAUSED'].includes(status()) || (ready && bad.length === 0);
        parts.push(b);
        if (bad.length) {
            const ul = node('ul', 'mt-3 space-y-1 text-sm');
            bad.forEach(r => ul.append(node('li', 'text-red-700', `Fila ${r.fila} · ${r.jugador}: ${r.documento.error}`)));
            parts.push(ul);
            parts.push(node('p', 'text-xs text-gray-500 mt-2', 'Corrige el dato en la tabla (volver al paso 2) y vuelve a generar. Las propuestas con error no se envían.'));
        }
        return card('1 · Propuestas en PDF', parts);
    }

    function sendCard() {
        const s = status();
        const parts = [];
        const canSend = s === 'DOCS_DONE' && withDoc().length > 0;
        const wrap = node('div', 'flex flex-wrap items-center gap-5 mb-3');
        [['whatsapp', 'WhatsApp'], ['email', 'Correo']].forEach(([id, label]) => {
            const l = node('label', 'flex items-center gap-2 text-sm text-gray-700 cursor-pointer');
            const c = node('input'); c.type = 'checkbox'; c.id = 'chk_' + id; c.checked = true; c.disabled = !canSend || st.busy;
            l.append(c, label);
            wrap.append(l);
        });
        parts.push(wrap);
        const b = btn('Enviar propuestas', 'bg-green-600 hover:bg-green-700 text-white', doSend);
        b.disabled = !canSend || st.busy;
        parts.push(b);
        if (!canSend && !st.snap) parts.push(node('p', 'text-xs text-gray-500 mt-2', 'Se habilita cuando todas las propuestas están generadas.'));
        parts.push(node('p', 'text-xs text-gray-500 mt-3',
            '«Aceptado» quiere decir que WhatsApp o Gmail recibieron el mensaje. Todavía no se muestra si el acudiente lo recibió o lo leyó.'));
        return card('2 · Envío', parts);
    }

    function progressCard() {
        const snap = st.snap;
        if (!snap) return null;
        const parts = [];
        const s = snap.status;
        if (s === 'PAUSED') {
            const box = banner('error', 'La campaña está EN PAUSA: no se está enviando nada. Casi siempre es el token de WhatsApp vencido o la contraseña de correo. Corrígelo en el servidor y reanuda.');
            const rb = btn('Reanudar envío', 'bg-red-600 hover:bg-red-700 text-white mt-3', doResume);
            rb.disabled = st.busy;
            box.append(node('div'), rb);
            parts.push(box);
        } else if (s === 'DISPATCHING') {
            parts.push(banner('info', `Enviando… ${snap.en_curso} entrega(s) en curso.`));
        }
        const channels = Object.keys(snap.progreso).sort();
        channels.forEach(ch => {
            const p = snap.progreso[ch];
            const sums = SEGMENTS.map(seg => ({ n: seg.states.reduce((a, k) => a + (p.estados[k] || 0), 0), cls: seg.cls, name: seg.name }));
            const row = node('div', 'mt-4');
            row.append(node('div', 'flex justify-between text-sm font-medium text-gray-700 mb-1', ''));
            row.firstChild.append(node('span', '', CHANNEL_NAME[ch] || ch), node('span', 'text-gray-500', sums.filter(x => x.n).map(x => `${x.n} ${x.name}`).join(' · ') || '—'));
            row.append(bar(sums, p.total));
            parts.push(row);
        });
        if (s === 'COMPLETED') parts.push(summary(snap));
        const failed = snap.entregas.filter(d => ['FAILED', 'NEEDS_REVIEW', 'RETRY_WAIT', 'UNCONFIRMED'].includes(d.status));
        const retryable = snap.entregas.filter(d => (d.status === 'FAILED' && !d.permanente) || ['NEEDS_REVIEW', 'UNCONFIRMED'].includes(d.status));
        if (retryable.length && s !== 'PAUSED') {
            const rb = btn(`Reenviar ${retryable.length} que no salieron`, 'bg-amber-500 hover:bg-amber-600 text-white mt-4', doRetry);
            rb.disabled = st.busy;
            parts.push(rb);
        }
        parts.push(deliveriesTable(snap.entregas, failed.length));
        return card('3 · Estado real de cada envío', parts);
    }

    function summary(snap) {
        const count = (ch, keys) => keys.reduce((a, k) => a + (((snap.progreso[ch] || {}).estados || {})[k] || 0), 0);
        const acc = ['ACCEPTED', 'SENT', 'DELIVERED', 'READ'];
        const wa = count('whatsapp', acc), em = count('email', acc);
        const failed = snap.entregas.filter(d => d.status === 'FAILED').length;
        const review = snap.entregas.filter(d => ['NEEDS_REVIEW', 'UNCONFIRMED'].includes(d.status)).length;
        const skipped = snap.entregas.filter(d => d.status === 'SKIPPED').length;
        const bits = [];
        if (snap.progreso.whatsapp) bits.push(`${wa} aceptadas por WhatsApp`);
        if (snap.progreso.email) bits.push(`${em} aceptadas por Gmail`);
        bits.push(`${failed} fallidas`);
        if (review) bits.push(`${review} por revisar`);
        bits.push(`${skipped} omitidas`);
        const kind = failed || review ? 'warn' : 'ok';
        return node('div', 'mt-4').appendChild(banner(kind, `Envío terminado: ${bits.join(', ')}.`)).parentNode;
    }

    function deliveriesTable(all, problems) {
        const wrap = node('div', 'mt-4');
        const bar = node('div', 'flex gap-2 mb-2');
        [['all', `Todas (${all.length})`], ['problems', `Con problemas (${problems})`]].forEach(([id, label]) => {
            const b = node('button', `px-3 py-1.5 rounded-full text-xs font-semibold border cursor-pointer ${st.filter === id ? 'bg-blue-600 text-white border-blue-600' : 'bg-white text-gray-600 border-gray-300 hover:bg-gray-50'}`, label);
            b.type = 'button';
            b.addEventListener('click', () => { st.filter = id; render(); });
            bar.append(b);
        });
        wrap.append(bar);
        const rows = st.filter === 'problems' ? all.filter(d => ['FAILED', 'NEEDS_REVIEW', 'RETRY_WAIT', 'UNCONFIRMED'].includes(d.status)) : all;
        const box = node('div', 'max-h-80 overflow-auto border border-gray-200 rounded-lg');
        const table = node('table', 'w-full text-left text-sm');
        const head = node('tr');
        ['Fila', 'Jugador', 'Canal', 'Estado', 'Detalle'].forEach(h => head.append(node('th', 'p-2 text-xs font-semibold text-gray-600', h)));
        const thead = node('thead', 'bg-gray-100 sticky top-0'); thead.append(head);
        const tbody = node('tbody');
        if (!rows.length) {
            const td = node('td', 'p-4 text-center text-gray-500', 'No hay entregas en este filtro.'); td.colSpan = 5;
            const tr = node('tr'); tr.append(td); tbody.append(tr);
        }
        rows.forEach(d => {
            const tr = node('tr', 'border-t border-gray-100');
            tr.append(node('td', 'p-2 text-xs text-gray-400', String(d.fila)), node('td', 'p-2', d.jugador), node('td', 'p-2 text-xs', CHANNEL_NAME[d.channel] || d.channel),
                node('td', `p-2 font-semibold ${STATUS_CLASS[d.status] || ''}`, d.label), node('td', 'p-2 text-xs text-gray-600', d.error || ''));
            tbody.append(tr);
        });
        table.append(thead, tbody); box.append(table); wrap.append(box);
        return wrap;
    }

    function render() {
        const root = document.getElementById('sendPanel');
        if (!root || !st.campaign) return;
        const kids = [];
        const head = node('div', 'text-sm text-gray-600');
        head.append(node('span', 'font-semibold text-gray-800', st.campaign.torneo), ` · estado: ${statusText(status())}`);
        kids.push(head);
        if (st.msg && st.msg.until && Date.now() > st.msg.until) st.msg = null;
        if (st.msg) kids.push(banner(st.msg.kind, st.msg.text));
        kids.push(docsCard(), sendCard());
        const pc = progressCard();
        if (pc) kids.push(pc);
        root.replaceChildren(...kids);
    }

    function statusText(s) {
        return { DRAFT: 'borrador', READY: 'lista', DOCS_RUNNING: 'generando propuestas', DOCS_DONE: 'propuestas listas', DISPATCHING: 'enviando',
            COMPLETED: 'envío terminado', PAUSED: 'en pausa', CANCELLED: 'cancelada' }[s] || s;
    }

    window.sendPanel = {
        open(id) {
            st.id = id; st.snap = null; st.msg = null; st.filter = 'all';
            refresh().catch(e => { st.msg = { kind: 'error', text: e.message }; });
        },
        stop,
    };
})();
