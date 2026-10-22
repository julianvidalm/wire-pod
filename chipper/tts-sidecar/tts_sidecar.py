"""piper text-to-speech sidecar for wire-pod's cortex Voice (pkg/cortex/piper).

Loads one piper voice (.onnx with its .onnx.json) per language once, then serves requests forever, so each
sentence costs only its synthesis (a fresh process spends about 1 s loading onnxruntime and a voice).

Protocol, one request at a time:
  stdout, once at start:  {"ready": true, "voices": {"es": 22050, "en": 22050}}   (language -> sample rate)
  stdin:                  {"lang": "es", "text": "Hola."}
  stdout:                 {"samples": N, "rate": 22050} followed by N raw mono s16le samples,
                          or {"error": "..."} with no samples.
Status goes to stderr. Exits at the end of stdin.
"""

import argparse
import json
import sys

import numpy as np


def load(path, threads):
    import onnxruntime as ort

    with open(path + ".json", encoding="utf-8") as f:
        cfg = json.load(f)
    so = ort.SessionOptions()
    if threads > 0:
        so.intra_op_num_threads = threads
    return cfg, ort.InferenceSession(path, sess_options=so, providers=["CPUExecutionProvider"])


def phoneme_ids(cfg, phonemes):
    m = cfg["phoneme_id_map"]
    ids = list(m["^"])
    for p in phonemes:
        if p in m:
            ids += m[p] + m["_"]
    return ids + m["$"]


def synthesize(voice, text):
    from piper_phonemize import phonemize_espeak

    cfg, session = voice
    inf = cfg["inference"]
    parts = []
    for sentence in phonemize_espeak(text, cfg["espeak"]["voice"]):
        x = np.array([phoneme_ids(cfg, sentence)], dtype=np.int64)
        feeds = {
            "input": x,
            "input_lengths": np.array([x.shape[1]], dtype=np.int64),
            "scales": np.array([inf["noise_scale"], inf["length_scale"], inf["noise_w"]], dtype=np.float32),
        }
        if cfg.get("num_speakers", 1) > 1:
            feeds["sid"] = np.array([0], dtype=np.int64)
        parts.append(session.run(None, feeds)[0].squeeze())
    audio = np.concatenate(parts) if parts else np.zeros(0, np.float32)
    return (np.clip(audio, -1, 1) * 32767).astype("<i2")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--voice", action="append", default=[], help="lang=path/to/voice.onnx (repeatable)")
    ap.add_argument("--threads", type=int, default=0, help="onnxruntime intra-op threads, 0 = its default")
    args = ap.parse_args()

    voices = {}
    for spec in args.voice:
        lang, _, path = spec.partition("=")
        voices[lang] = load(path, args.threads)
        synthesize(voices[lang], "Hola." if lang == "es" else "Hello.")  # warm up
    out = sys.stdout.buffer
    rates = {lang: v[0]["audio"]["sample_rate"] for lang, v in voices.items()}
    out.write((json.dumps({"ready": True, "voices": rates}) + "\n").encode())
    out.flush()
    print(f"tts-sidecar: ready, voices {rates}", file=sys.stderr, flush=True)

    for line in sys.stdin.buffer:
        try:
            req = json.loads(line)
            voice = voices.get(req.get("lang"))
            if voice is None:
                raise ValueError(f"no voice for language {req.get('lang')!r}")
            pcm = synthesize(voice, req.get("text", ""))
            head = {"samples": len(pcm), "rate": voice[0]["audio"]["sample_rate"]}
            out.write((json.dumps(head) + "\n").encode() + pcm.tobytes())
        except Exception as e:  # one bad request must not stop the sidecar
            out.write((json.dumps({"error": str(e)}) + "\n").encode())
        out.flush()


if __name__ == "__main__":
    try:
        main()
    except (BrokenPipeError, KeyboardInterrupt):
        sys.exit(0)
