package git

import "testing"

func TestParseUnifiedDiff(t *testing.T) {
	raw := `diff --git a/lib/main.dart b/lib/main.dart
index 1234..5678 100644
--- a/lib/main.dart
+++ b/lib/main.dart
@@ -1,3 +1,4 @@
 import 'app.dart';
-void main() {}
+Future<void> main() async {
+  runApp();
+}
diff --git a/assets/icon.png b/assets/icon.png
Binary files a/assets/icon.png and b/assets/icon.png differ
`
	files := ParseUnifiedDiff(raw)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0].Path != "lib/main.dart" || files[0].Additions != 3 || files[0].Deletions != 1 {
		t.Fatalf("unexpected diff stats: %+v", files[0])
	}
	if len(files[0].Hunks) != 1 || len(files[0].Hunks[0].Lines) != 5 {
		t.Fatalf("unexpected hunks: %+v", files[0].Hunks)
	}
	if files[1].Path != "assets/icon.png" || len(files[1].Hunks) != 0 {
		t.Fatalf("binary file should have 0 hunks, got: %+v", files[1])
	}
}
