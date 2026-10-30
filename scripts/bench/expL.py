import torch, torch.nn as nn, json
from transformers import CLIPModel
MID = "OFA-Sys/chinese-clip-vit-large-patch14"
m = CLIPModel.from_pretrained(MID).eval()
vis, proj = m.vision_model, m.visual_projection
class Wrap(nn.Module):
    def __init__(self, v, p): super().__init__(); self.v = v; self.p = p
    def forward(self, pixel_values):
        return self.p(self.v(pixel_values=pixel_values).pooler_output)
w = Wrap(vis, proj).eval()
x = torch.randn(1, 3, 224, 224)
with torch.no_grad():
    y = w(x)
print("out:", tuple(y.shape), "params: %.1fM" % (sum(p.numel() for p in w.parameters())/1e6))
torch.onnx.export(w, (x,), "/tmp/bench/m/vitl14.onnx",
    input_names=["pixel_values"], output_names=["image_embeds"],
    dynamic_axes={"pixel_values": {0: "batch_size"}, "image_embeds": {0: "batch_size"}},
    opset_version=17)
json.dump({"model": MID, "dim": int(y.shape[-1])}, open("/tmp/bench/m/meta_l14.json", "w"))
print("exported")
