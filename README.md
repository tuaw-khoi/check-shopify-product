# Shopify Price Checker

Web app chạy local để đọc catalog công khai của Shopify, hiển thị thông tin sản phẩm, hình ảnh, giá từng variant, xuất snapshot `.xlsx` và tạo `.csv` để import sản phẩm vào Shopify.

## Chạy bản đã build

1. Mở `shopify-price-checker.exe`.
2. Giữ cửa sổ console đang mở; dashboard sẽ tự mở trong trình duyệt tại địa chỉ `127.0.0.1`.
3. Nhập URL store, bấm **Kiểm tra store**, chọn phạm vi rồi bấm **Bắt đầu quét**.
4. Dùng bộ lọc trên bảng, bấm **Tải Excel phân tích** hoặc **Tải CSV import Shopify** khi scan hoàn tất.
5. Đóng bằng `Ctrl+C` trong cửa sổ console.

Không cần cài Go, Python, Node.js hay Shopify app. Tool không ghi dữ liệu vào store và không gửi dữ liệu tới server trung gian.

## Phạm vi dữ liệu

- Dữ liệu lấy từ sitemap, collection storefront và Ajax Product API công khai.
- Dashboard có bảng product riêng với title, description, vendor, type, tags, collections, options, ngày đăng, ảnh và khoảng giá.
- Bảng variant giữ một dòng cho mỗi variant; ảnh variant được ưu tiên khi storefront cung cấp.
- Ảnh được lưu dưới dạng URL/hyperlink và thumbnail, không tải file ảnh nhị phân về máy.
- Giá được đọc theo locale/currency mà URL hiện tại trả về.
- Tồn kho chỉ có trạng thái còn/hết/không rõ, không có số lượng chính xác.
- Không bao gồm draft, store có mật khẩu, giá sau coupon/checkout hoặc selling-plan price.
- Ajax Product API có thể giới hạn 250 variants cho một product; tool sẽ tạo cảnh báo nếu chạm ngưỡng này.

## File Excel

Workbook có năm sheet: `Summary`, `Products`, `Images`, `Variants`, `Errors`. `Products` chứa cả mô tả plain text và HTML; `Images` có một dòng cho mỗi ảnh. Các cột giá là số, không phải chuỗi, nên có thể tiếp tục lọc và tính toán trong Excel.

## File import Shopify

Nút **Tải CSV import Shopify** tạo file UTF-8 theo mẫu Product CSV hiện hành của Shopify. File bao gồm title, handle, description, vendor, product type, tags, tối đa ba option, SKU, barcode, giá, compare-at price, ảnh product và ảnh variant.

- Shopify chỉ nhận `.csv` cho chức năng import product, không nhận trực tiếp `.xlsx`.
- Ảnh được import bằng URL công khai từ store nguồn; Shopify sẽ tải lại ảnh khi import.
- Storefront công khai không cung cấp số lượng tồn kho, cost, cân nặng, thuế hoặc trạng thái inventory tracking chính xác, nên các cột này được bỏ khỏi CSV thay vì tự đoán dữ liệu.
- Shopify giới hạn một file import product ở 15 MB. Nếu file vượt giới hạn, hãy quét theo collection hoặc cụm tên nhỏ hơn.
- Trước khi import số lượng lớn, nên thử một nhóm product nhỏ và kiểm tra màn hình review của Shopify.

## Build từ source

Yêu cầu Go 1.27 trở lên:

```powershell
.\build.ps1
```

Script sẽ chạy toàn bộ test trước khi tạo `shopify-price-checker.exe`. Project chỉ dùng Go standard library; CSS và JavaScript được nhúng vào executable.

## API local

- `POST /api/discover`
- `POST /api/scans`
- `GET /api/scans/current`
- `POST /api/scans/current/cancel`
- `GET /api/scans/current/export.xlsx`
- `GET /api/scans/current/export-shopify.csv`

Server chỉ bind loopback và từ chối Host/remote address không phải local.
