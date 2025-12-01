import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { useTranslation } from "react-i18next";
import { Rocket } from "lucide-react";

interface OnboardingModalProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onConfigureProvider: () => void;
}

/**
 * OnboardingModal component
 * Displays a welcome dialog when no providers are configured,
 * guiding users to configure their first provider.
 */
export function OnboardingModal({
    open,
    onOpenChange,
    onConfigureProvider,
}: OnboardingModalProps) {
    const { t } = useTranslation();

    const handleConfigureClick = () => {
        onConfigureProvider();
        onOpenChange(false);
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-md">
                <DialogHeader>
                    <div className="flex items-center justify-center mb-4">
                        <div className="rounded-full bg-primary/10 p-3">
                            <Rocket className="h-8 w-8 text-primary" />
                        </div>
                    </div>
                    <DialogTitle className="text-center text-xl">
                        {t("onboarding_welcome_title")}
                    </DialogTitle>
                    <DialogDescription className="text-center pt-2">
                        {t("onboarding_welcome_description")}
                    </DialogDescription>
                </DialogHeader>
                <DialogFooter className="sm:justify-center">
                    <Button onClick={handleConfigureClick} className="w-full sm:w-auto">
                        {t("onboarding_configure_button")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
