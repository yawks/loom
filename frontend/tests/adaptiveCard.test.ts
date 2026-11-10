import { strict as assert } from "node:assert";
import { test } from "node:test";
import { adaptiveColumnStyle, adaptiveImageStyle, adaptiveSpacingClass, hasStructuredAdaptiveCard, safeAdaptiveActionURL, shouldHideAdaptiveCardBody } from "../src/lib/adaptiveCard.ts";

test("supplementary cards preserve accompanying text; legacy standalone cards hide fallback", () => {
  const card = { type: "adaptive_card", cardJson: '{"type":"AdaptiveCard","body":[]}' };
  const legacy = JSON.stringify([card]);
  const supplementary = JSON.stringify([{ ...card, isSupplementary: true }]);
  assert.ok(hasStructuredAdaptiveCard(legacy));
  assert.ok(hasStructuredAdaptiveCard(supplementary));
  assert.ok(shouldHideAdaptiveCardBody(legacy));
  assert.equal(shouldHideAdaptiveCardBody(supplementary), false);
  for (const value of [undefined, "", "null", "{}", "invalid", '[null,{}]', '[{"type":"image"}]']) {
    assert.equal(hasStructuredAdaptiveCard(value), false);
    assert.equal(shouldHideAdaptiveCardBody(value), false);
  }
});

test("card columns retain pixel/auto widths instead of equal-width stretching", () => {
  assert.equal(adaptiveColumnStyle({ width: "34px" }).flex, "0 0 34px");
  assert.equal(adaptiveColumnStyle({ width: "47px" }).width, 47);
  assert.equal(adaptiveColumnStyle({ width: "auto" }).flex, "0 0 auto");
  assert.equal(adaptiveColumnStyle({ width: "stretch" }).flex, "1 1 0px");
  assert.equal(adaptiveColumnStyle({ width: 2 }).flex, "2 1 0px");
  assert.equal(adaptiveColumnStyle({ width: "34px", verticalContentAlignment: "center" }).justifyContent, "center");
  assert.deepEqual(adaptiveImageStyle({ width: "13px", height: "13px" }), { width: 13, height: 13, maxWidth: "100%" });
  assert.deepEqual(adaptiveImageStyle({ width: "url(evil)", height: "-10px" }), { maxWidth: "100%" });
  assert.equal(adaptiveSpacingClass({ spacing: "None" }), "mt-0");
  assert.equal(adaptiveSpacingClass({ spacing: "small" }), "mt-1");
});

test("only safe browser actions can be enabled", () => {
  assert.equal(safeAdaptiveActionURL("https://example.com/issue"), "https://example.com/issue");
  for (const value of ["javascript:alert(1)", "file:///etc/passwd", "data:text/html,evil", "bad", undefined]) {
    assert.equal(safeAdaptiveActionURL(value), undefined);
  }
});
