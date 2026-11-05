#!/usr/bin/env python3
"""Genera testdata/python_cases.json: casos y resultados de la lógica de reemplazo del CLIENTE.

Ejecuta, sin modificarlas, las funciones puras de MajesticBecas/becas/generador.py
(`_reemplazar`, `es_encabezado`, `formato_euros`) y sus expresiones regulares (RE_X, RE_FECHA_X,
RE_TORNEO, RE_PLACEHOLDER). Se extraen del archivo con `ast`, así NO hace falta python-docx.
El test de Go compara su implementación contra estos resultados.

Uso:  python3 gen_cases.py "<ruta>/MajesticBecas/majestic_becas/becas/generador.py" > python_cases.json
Solo usa la biblioteca estándar. Es determinista (semilla fija).
"""
import ast
import json
import random
import re
import sys

ruta = sys.argv[1]
fuente = open(ruta, encoding="utf-8").read()
arbol = ast.parse(fuente)

NOMBRES_ASIGNACION = {"RE_X", "RE_FECHA_X", "RE_TORNEO", "RE_PLACEHOLDER"}
NOMBRES_FUNCION = {"_reemplazar", "es_encabezado", "formato_euros"}
nodos = []
for n in arbol.body:
    if isinstance(n, ast.Assign) and len(n.targets) == 1 and getattr(n.targets[0], "id", None) in NOMBRES_ASIGNACION:
        nodos.append(n)
    elif isinstance(n, ast.FunctionDef) and n.name in NOMBRES_FUNCION:
        nodos.append(n)
ns = {"re": re}
exec(compile(ast.Module(body=nodos, type_ignores=[]), ruta, "exec"), ns)  # código del cliente, tal cual
assert {"RE_X", "RE_FECHA_X", "RE_TORNEO", "RE_PLACEHOLDER", "_reemplazar", "es_encabezado", "formato_euros"} <= set(ns)

PATRONES = {
    "x": ns["RE_X"].pattern,
    "fecha": ns["RE_FECHA_X"].pattern,
    "torneo": ns["RE_TORNEO"].pattern,
    "placeholder": ns["RE_PLACEHOLDER"].pattern,
    "xxx": r"X{3}",
}


class Run:  # imita lo único que _reemplazar usa de un run de python-docx: el atributo .text
    def __init__(self, t):
        self.text = t


class Par:
    def __init__(self, textos):
        self.runs = [Run(t) for t in textos]


rng = random.Random(20261105)
PIEZAS = ["TORNEO", "TORNEO ", " ", "  ", "\t", "X", "XX", "XXX", "XXXX", "XXXXXXXX", "/", "Fecha:", "a", "Ñ", "€", "é", "😀", "XX/XX/XXXX",
          "Jugador", ":", "(+57) ", "TORNEOS", "torneo ", "\n"]


def texto_aleatorio():
    return "".join(rng.choice(PIEZAS) for _ in range(rng.randint(0, 9)))


def partir(texto):
    """Parte el texto en runs en posiciones aleatorias (puede haber runs vacíos)."""
    if not texto:
        return [""] * rng.randint(0, 2) or [""]
    cortes = sorted(rng.randint(0, len(texto)) for _ in range(rng.randint(0, 5)))
    pedazos, ant = [], 0
    for c in cortes + [len(texto)]:
        pedazos.append(texto[ant:c])
        ant = c
    return pedazos


casos_regex = []
for _ in range(2500):
    t = texto_aleatorio()
    fila = {"texto": t, "encabezado": ns["es_encabezado"](t)}
    for nombre, patron in PATRONES.items():
        m = re.search(patron, t)
        fila[nombre] = [m.start(), m.end()] if m else None
    casos_regex.append(fila)

casos_reemplazo = []
for _ in range(2500):
    t = texto_aleatorio()
    runs = partir(t)
    nombre = rng.choice(list(PATRONES))
    nuevo = rng.choice(["", "Z", "COPA MAJESTIC", "Ñandú €1.960", "05/11/2026", "a\tb", " x "])
    par = Par(runs)
    ok = ns["_reemplazar"](par, PATRONES[nombre], nuevo)
    casos_reemplazo.append({"runs": runs, "patron": nombre, "nuevo": nuevo, "ok": bool(ok), "resultado": [r.text for r in par.runs]})

euros = [0, 1, 9, 10, 99, 100, 200, 495, 650, 990, 999, 1000, 1550, 1960, 2800, 12345, 100000, 1234567] + [rng.randint(0, 5_000_000) for _ in range(200)]
casos_euros = [{"n": n, "texto": ns["formato_euros"](n)} for n in euros]

json.dump({"patrones": PATRONES, "regex": casos_regex, "reemplazo": casos_reemplazo, "euros": casos_euros},
          sys.stdout, ensure_ascii=False, separators=(",", ":"))
